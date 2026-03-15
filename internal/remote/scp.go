package remote

import (
	"fmt"
	"os/exec"
	"strings"
)

type scpTransport struct {
	info connInfo
}

func newSCP(info connInfo) Transport {
	return &scpTransport{info: info}
}

func (t *scpTransport) Check() error {
	if !hasExecutable("ssh") {
		return fmt.Errorf("ssh binary not found")
	}
	if !hasExecutable("scp") {
		return fmt.Errorf("scp binary not found")
	}
	return checkSSHConnectivity(t.info)
}

func (t *scpTransport) Close() error {
	return nil
}

func (t *scpTransport) FindRoots(basePath string) (*Roots, error) {
	return cliFindRoots(t.info, basePath)
}

func (t *scpTransport) UploadTree(localRoot, remoteRoot string, preserveConfig bool) error {
	return uploadTreeWalk(localRoot, remoteRoot, preserveConfig, t.ensureDir, t.copyFile, t.remotePathExists)
}

func (t *scpTransport) RemoveDirIfExists(remotePath string) error {
	return cliRemoveDirIfExists(t.info, remotePath)
}

// --- private helpers ---

func (t *scpTransport) ensureDir(remoteDir string) error {
	_, err := runRemoteCommand(t.info, "mkdir -p "+shQuote(remoteDir))
	return err
}

func (t *scpTransport) copyFile(localPath, remotePath string) error {
	args := scpCLIBaseArgs(t.info.sshKey)
	args = append(args, localPath, fmt.Sprintf("%s:%s", t.info.rawTarget, remotePath))
	out, err := exec.Command("scp", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy %s to %s via scp failed: %w (%s)", localPath, remotePath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (t *scpTransport) remotePathExists(remotePath string) (bool, error) {
	return cliRemotePathExists(t.info, remotePath)
}
