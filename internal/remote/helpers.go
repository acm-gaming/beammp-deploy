package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// parseSSHTarget resolves a raw SSH target (user@host or ssh-config alias)
// into a username and host:port pair.
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

func expandHomePathForCurrentUser(pathValue string) string {
	currentUser, err := user.Current()
	if err != nil {
		return strings.TrimSpace(pathValue)
	}
	return expandHomePath(pathValue, currentUser.HomeDir)
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

// rewriteConfigTarget decides whether a remote path should be rewritten or
// skipped for config-file preservation. The pathExists callback lets each
// transport supply its own existence check.
func rewriteConfigTarget(remotePath string, preserve bool, pathExists func(string) (bool, error)) (string, bool, error) {
	if !preserve {
		return remotePath, false, nil
	}

	base := path.Base(remotePath)
	lower := strings.ToLower(base)
	switch lower {
	case "config.json", "config.ini", "config.toml":
		exists, err := pathExists(remotePath)
		if err != nil {
			return "", false, err
		}
		if exists {
			return "", true, nil
		}
		return remotePath, false, nil
	case "config.json.example", "config.ini.example", "config.toml.example":
		target := strings.TrimSuffix(remotePath, ".example")
		exists, err := pathExists(target)
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

func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func hasExecutable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
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

// sshCLIBaseArgs returns the base arguments for an ssh CLI invocation.
func sshCLIBaseArgs(sshKey string) []string {
	args := []string{"-o", "BatchMode=yes"}
	if strings.TrimSpace(sshKey) != "" {
		args = append(args, "-i", sshKey)
	}
	return args
}

// scpCLIBaseArgs returns the base arguments for an scp CLI invocation.
func scpCLIBaseArgs(sshKey string) []string {
	args := []string{"-q", "-o", "BatchMode=yes"}
	if strings.TrimSpace(sshKey) != "" {
		args = append(args, "-i", sshKey)
	}
	return args
}

// runRemoteCommand executes a shell script on the remote host via the ssh CLI.
func runRemoteCommand(info connInfo, script string) ([]byte, error) {
	args := sshCLIBaseArgs(info.sshKey)
	args = append(args, info.rawTarget, "sh -lc "+shQuote(script))
	cmd := exec.Command("ssh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("ssh command failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// checkSSHConnectivity verifies that the ssh CLI can reach the remote host.
func checkSSHConnectivity(info connInfo) error {
	args := sshCLIBaseArgs(info.sshKey)
	args = append(args, info.rawTarget, "true")
	out, err := exec.Command("ssh", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh connectivity check failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// cliFindRoots discovers Server/Client directories via the ssh CLI.
func cliFindRoots(info connInfo, basePath string) (*Roots, error) {
	out, err := runRemoteCommand(info, "find "+shQuote(basePath)+" -maxdepth 2 -type d")
	if err != nil {
		return nil, fmt.Errorf("scan remote path %s: %w", basePath, err)
	}
	paths := filterLines(string(out))
	return resolveRootsFromPaths(paths, basePath)
}

// cliRemoveDirIfExists removes a remote directory via the ssh CLI.
func cliRemoveDirIfExists(info connInfo, remotePath string) error {
	_, err := runRemoteCommand(info, "rm -rf -- "+shQuote(remotePath))
	return err
}

// cliRemotePathExists checks whether a remote path exists via the ssh CLI.
func cliRemotePathExists(info connInfo, remotePath string) (bool, error) {
	_, err := runRemoteCommand(info, "test -e "+shQuote(remotePath))
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// uploadTreeWalk walks a local directory tree and uploads files to a remote
// root, using the provided callbacks for directory creation and file copying.
func uploadTreeWalk(
	localRoot, remoteRoot string,
	preserveConfig bool,
	ensureDir func(string) error,
	copyFile func(localPath, remotePath string) error,
	pathExists func(string) (bool, error),
) error {
	if err := ensureDir(remoteRoot); err != nil {
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
			return ensureDir(remotePath)
		}

		remotePath, skip, rwErr := rewriteConfigTarget(remotePath, preserveConfig, pathExists)
		if rwErr != nil {
			return rwErr
		}
		if skip {
			return nil
		}

		if err := ensureDir(path.Dir(remotePath)); err != nil {
			return err
		}
		return copyFile(localPath, remotePath)
	})
}
