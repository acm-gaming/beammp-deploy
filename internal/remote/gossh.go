package remote

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type goSSHTransport struct {
	info       connInfo
	sshClient  *ssh.Client
	sftpClient *sftp.Client
}

func newGoSSH(info connInfo) Transport {
	return &goSSHTransport{info: info}
}

func (t *goSSHTransport) Check() error {
	if t.info.userName == "" || t.info.hostPort == "" {
		return fmt.Errorf("go-ssh requires parsed user and host:port")
	}

	authMethods, err := loadAuthMethods(t.info.sshKey)
	if err != nil {
		return fmt.Errorf("prepare ssh auth methods: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User:            t.info.userName,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	// Use explicit IPv4 for IPv4 literals to avoid IPv6 route issues.
	network := "tcp"
	hostOnly, _, splitErr := net.SplitHostPort(t.info.hostPort)
	if splitErr == nil && net.ParseIP(strings.Trim(hostOnly, "[]")) != nil && strings.Contains(hostOnly, ".") {
		network = "tcp4"
	}

	conn, err := ssh.Dial(network, t.info.hostPort, cfg)
	if err != nil {
		return fmt.Errorf("connect to %s (resolved user=%s network=%s address=%s): %w",
			t.info.rawTarget, t.info.userName, network, t.info.hostPort, err)
	}

	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open sftp for %s: %w", t.info.rawTarget, err)
	}

	t.sshClient = conn
	t.sftpClient = sftpClient
	return nil
}

func (t *goSSHTransport) Close() error {
	var errs []error
	if t.sftpClient != nil {
		if err := t.sftpClient.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if t.sshClient != nil {
		if err := t.sshClient.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *goSSHTransport) FindRoots(basePath string) (*Roots, error) {
	paths, err := t.walkDirs(basePath, 2)
	if err != nil {
		return nil, err
	}
	return resolveRootsFromPaths(paths, basePath)
}

func (t *goSSHTransport) UploadTree(localRoot, remoteRoot string, preserveConfig bool) error {
	return uploadTreeWalk(localRoot, remoteRoot, preserveConfig, t.ensureDir, t.copyFile, t.remotePathExists)
}

func (t *goSSHTransport) RemoveDirIfExists(remotePath string) error {
	return t.removeRecursive(remotePath)
}

// --- private helpers ---

func (t *goSSHTransport) walkDirs(root string, depth int) ([]string, error) {
	var results []string
	var walk func(string, int) error
	walk = func(curr string, level int) error {
		info, err := t.sftpClient.Stat(curr)
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
		children, err := t.sftpClient.ReadDir(curr)
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

func (t *goSSHTransport) removeRecursive(target string) error {
	info, err := t.sftpClient.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if !info.IsDir() {
		return t.sftpClient.Remove(target)
	}

	entries, err := t.sftpClient.ReadDir(target)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entryPath := path.Join(target, entry.Name())
		if err := t.removeRecursive(entryPath); err != nil {
			return err
		}
	}
	return t.sftpClient.RemoveDirectory(target)
}

func (t *goSSHTransport) ensureDir(remoteDir string) error {
	return t.sftpClient.MkdirAll(remoteDir)
}

func (t *goSSHTransport) copyFile(localPath, remotePath string) error {
	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file %s: %w", localPath, err)
	}
	defer src.Close()

	dst, err := t.sftpClient.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open remote file %s: %w", remotePath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy %s to %s: %w", localPath, remotePath, err)
	}
	return nil
}

func (t *goSSHTransport) remotePathExists(remotePath string) (bool, error) {
	_, err := t.sftpClient.Stat(remotePath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
