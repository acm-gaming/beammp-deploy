package remote

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type Client struct {
	mode      string
	sshTarget string
	sshKey    string

	sshClient  *ssh.Client
	sftpClient *sftp.Client
}

type Roots struct {
	Server string
	Client string
}

func Connect(sshTarget string, sshKeyPath string) (*Client, error) {
	expandedKey := expandHomePathForCurrentUser(sshKeyPath)
	if hasExecutable("ssh") && hasExecutable("scp") {
		return &Client{
			mode:      "system",
			sshTarget: sshTarget,
			sshKey:    expandedKey,
		}, nil
	}

	userName, hostPort, err := parseSSHTarget(sshTarget)
	if err != nil {
		return nil, err
	}

	authMethods, err := loadAuthMethods(expandedKey)
	if err != nil {
		return nil, fmt.Errorf("prepare ssh auth methods: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User:            userName,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	// Use explicit IPv4 for IPv4 literals to avoid IPv6 route issues.
	network := "tcp"
	hostOnly, _, splitErr := net.SplitHostPort(hostPort)
	if splitErr == nil && net.ParseIP(strings.Trim(hostOnly, "[]")) != nil && strings.Contains(hostOnly, ".") {
		network = "tcp4"
	}

	conn, err := ssh.Dial(network, hostPort, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to %s (resolved user=%s network=%s address=%s): %w", sshTarget, userName, network, hostPort, err)
	}

	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open sftp for %s: %w", sshTarget, err)
	}

	return &Client{
		mode:       "go",
		sshTarget:  sshTarget,
		sshKey:     expandedKey,
		sshClient:  conn,
		sftpClient: sftpClient,
	}, nil
}

func (c *Client) Close() error {
	if c.mode == "system" {
		return nil
	}

	var errs []error
	if c.sftpClient != nil {
		if err := c.sftpClient.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.sshClient != nil {
		if err := c.sshClient.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Client) FindRoots(basePath string) (*Roots, error) {
	if c.mode == "system" {
		out, err := c.runRemoteScript("find " + shQuote(basePath) + " -maxdepth 2 -type d")
		if err != nil {
			return nil, fmt.Errorf("scan remote path %s: %w", basePath, err)
		}
		paths := filterLines(string(out))
		return resolveRootsFromPaths(paths, basePath)
	}

	paths, err := c.walkDirs(basePath, 2)
	if err != nil {
		return nil, err
	}
	return resolveRootsFromPaths(paths, basePath)
}

func (c *Client) UploadTree(localRoot, remoteRoot string, preserveConfig bool) error {
	if err := c.ensureDir(remoteRoot); err != nil {
		return err
	}

	return filepath.WalkDir(localRoot, func(localPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localRoot, localPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		remotePath := path.Join(remoteRoot, filepath.ToSlash(rel))
		if d.IsDir() {
			return c.ensureDir(remotePath)
		}

		remotePath, skip, err := c.rewriteConfigTarget(remotePath, preserveConfig)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}

		if err := c.ensureDir(path.Dir(remotePath)); err != nil {
			return err
		}
		return c.copyFile(localPath, remotePath)
	})
}

func (c *Client) RemoveDirIfExists(remotePath string) error {
	if c.mode == "system" {
		_, err := c.runRemoteScript("rm -rf -- " + shQuote(remotePath))
		return err
	}

	_, err := c.sftpClient.Stat(remotePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return c.removeRecursive(remotePath)
}

func (c *Client) walkDirs(root string, depth int) ([]string, error) {
	var results []string
	var walk func(string, int) error
	walk = func(curr string, level int) error {
		info, err := c.sftpClient.Stat(curr)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		results = append(results, curr)
		if level >= depth {
			return nil
		}
		children, err := c.sftpClient.ReadDir(curr)
		if err != nil {
			return err
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			if err := walk(path.Join(curr, child.Name()), level+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, fmt.Errorf("scan remote path %s: %w", root, err)
	}
	return results, nil
}

func (c *Client) removeRecursive(target string) error {
	info, err := c.sftpClient.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if !info.IsDir() {
		return c.sftpClient.Remove(target)
	}

	entries, err := c.sftpClient.ReadDir(target)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entryPath := path.Join(target, entry.Name())
		if err := c.removeRecursive(entryPath); err != nil {
			return err
		}
	}
	return c.sftpClient.RemoveDirectory(target)
}

func (c *Client) rewriteConfigTarget(remotePath string, preserve bool) (string, bool, error) {
	if !preserve {
		return remotePath, false, nil
	}

	base := path.Base(remotePath)
	lower := strings.ToLower(base)
	switch lower {
	case "config.json", "config.ini", "config.toml":
		exists, err := c.remotePathExists(remotePath)
		if err != nil {
			return "", false, err
		}
		if exists {
			return "", true, nil
		}
		return remotePath, false, nil
	case "config.json.example", "config.ini.example", "config.toml.example":
		target := strings.TrimSuffix(remotePath, ".example")
		exists, err := c.remotePathExists(target)
		if err != nil {
			return "", false, err
		}
		if exists {
			return "", true, nil
		}
		return target, false, nil
	default:
		return remotePath, false, nil
	}
}

func (c *Client) ensureDir(remoteDir string) error {
	if c.mode == "system" {
		_, err := c.runRemoteScript("mkdir -p " + shQuote(remoteDir))
		return err
	}
	return c.sftpClient.MkdirAll(remoteDir)
}

func (c *Client) copyFile(localPath, remotePath string) error {
	if c.mode == "system" {
		args := c.scpBaseArgs()
		args = append(args, localPath, fmt.Sprintf("%s:%s", c.sshTarget, remotePath))
		out, err := exec.Command("scp", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("copy %s to %s via scp failed: %w (%s)", localPath, remotePath, err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file %s: %w", localPath, err)
	}
	defer src.Close()

	dst, err := c.sftpClient.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open remote file %s: %w", remotePath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy %s to %s: %w", localPath, remotePath, err)
	}
	return nil
}

func parseSSHTarget(raw string) (userName, hostPort string, err error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", "", fmt.Errorf("invalid ssh target %q", raw)
	}

	var explicitUser string
	hostPart := target
	if at := strings.LastIndex(target, "@"); at >= 0 {
		explicitUser = strings.TrimSpace(target[:at])
		hostPart = strings.TrimSpace(target[at+1:])
	}
	if hostPart == "" {
		return "", "", fmt.Errorf("invalid ssh target %q", raw)
	}

	lookupHost := hostPart
	explicitPort := ""
	if h, p, splitErr := net.SplitHostPort(hostPart); splitErr == nil {
		lookupHost = h
		explicitPort = p
	}

	normalizedLookupHost := strings.Trim(lookupHost, "[]")
	isLiteralIP := net.ParseIP(normalizedLookupHost) != nil

	configHost := lookupHost
	port := explicitPort
	userName = explicitUser

	if !isLiteralIP {
		if resolvedHost, resolvedUser, resolvedPort, ok := resolveViaSSHCLI(lookupHost); ok {
			if resolvedHost != "" {
				configHost = resolvedHost
			}
			if userName == "" && resolvedUser != "" {
				userName = resolvedUser
			}
			if port == "" && resolvedPort != "" {
				port = resolvedPort
			}
		}

		if configuredHost := strings.TrimSpace(ssh_config.Get(lookupHost, "Hostname")); configuredHost != "" {
			configHost = configuredHost
		}
		if port == "" {
			port = strings.TrimSpace(ssh_config.Get(lookupHost, "Port"))
		}
		if userName == "" {
			userName = strings.TrimSpace(ssh_config.Get(lookupHost, "User"))
		}
	}

	if port == "" {
		port = "22"
	}
	if userName == "" {
		return "", "", fmt.Errorf("invalid ssh target %q: no user provided and no User in ssh config", raw)
	}

	hostPort = net.JoinHostPort(configHost, port)
	return userName, hostPort, nil
}

func resolveViaSSHCLI(host string) (hostname, userName, port string, ok bool) {
	out, err := exec.Command("ssh", "-G", host).Output()
	if err != nil {
		return "", "", "", false
	}
	lines := strings.Split(string(out), "\n")
	values := map[string]string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.ToLower(parts[0])
		val := strings.TrimSpace(strings.Join(parts[1:], " "))
		if val == "" {
			continue
		}
		if _, exists := values[key]; !exists {
			values[key] = val
		}
	}
	return values["hostname"], values["user"], values["port"], true
}

func loadAuthMethods(sshKeyPath string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			ag := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(ag.Signers))
		}
	}

	currentUser, err := user.Current()
	if err == nil {
		keyPaths := buildKeyPathCandidates(currentUser.HomeDir, sshKeyPath)
		for _, keyPath := range keyPaths {
			keyData, readErr := os.ReadFile(keyPath)
			if readErr != nil {
				continue
			}
			signer, parseErr := ssh.ParsePrivateKey(keyData)
			if parseErr != nil {
				continue
			}
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	if len(methods) == 0 {
		return nil, errors.New("no SSH auth method available; configure SSH agent or provide ssh_key (defaults to ~/.ssh/id_rsa)")
	}
	return methods, nil
}

func buildKeyPathCandidates(homeDir, configured string) []string {
	candidates := make([]string, 0, 4)
	if p := expandHomePath(configured, homeDir); strings.TrimSpace(p) != "" {
		candidates = append(candidates, p)
	}

	defaults := []string{
		filepath.Join(homeDir, ".ssh", "id_rsa"),
		filepath.Join(homeDir, ".ssh", "id_ed25519"),
	}
	for _, p := range defaults {
		if !slices.Contains(candidates, p) {
			candidates = append(candidates, p)
		}
	}
	return candidates
}

func expandHomePath(pathValue, homeDir string) string {
	p := strings.TrimSpace(pathValue)
	if p == "" {
		return ""
	}
	if p == "~" {
		return homeDir
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir, strings.TrimPrefix(p, "~/"))
	}
	return p
}

func resolveRootsFromPaths(paths []string, basePath string) (*Roots, error) {
	var serverPath string
	var clientPath string
	for _, p := range paths {
		name := strings.ToLower(path.Base(p))
		switch name {
		case "server":
			if serverPath == "" || len(p) < len(serverPath) {
				serverPath = p
			}
		case "client":
			if clientPath == "" || len(p) < len(clientPath) {
				clientPath = p
			}
		}
	}

	if serverPath == "" || clientPath == "" {
		return nil, fmt.Errorf("could not find Server and Client directories within depth 2 of %s", basePath)
	}

	return &Roots{
		Server: serverPath,
		Client: clientPath,
	}, nil
}

func (c *Client) remotePathExists(remotePath string) (bool, error) {
	if c.mode == "system" {
		_, err := c.runRemoteScript("test -e " + shQuote(remotePath))
		if err == nil {
			return true, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}

	_, err := c.sftpClient.Stat(remotePath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (c *Client) runRemoteScript(script string) ([]byte, error) {
	args := c.sshBaseArgs()
	args = append(args, c.sshTarget, "sh -lc "+shQuote(script))
	cmd := exec.Command("ssh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("ssh command failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (c *Client) sshBaseArgs() []string {
	args := []string{"-o", "BatchMode=yes"}
	if strings.TrimSpace(c.sshKey) != "" {
		args = append(args, "-i", c.sshKey)
	}
	return args
}

func (c *Client) scpBaseArgs() []string {
	args := []string{"-q", "-o", "BatchMode=yes"}
	if strings.TrimSpace(c.sshKey) != "" {
		args = append(args, "-i", c.sshKey)
	}
	return args
}

func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func hasExecutable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func expandHomePathForCurrentUser(pathValue string) string {
	currentUser, err := user.Current()
	if err != nil {
		return strings.TrimSpace(pathValue)
	}
	return expandHomePath(pathValue, currentUser.HomeDir)
}

func filterLines(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
