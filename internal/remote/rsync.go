package remote

import (
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

type rsyncTransport struct {
	info connInfo
}

func newRsync(info connInfo) Transport {
	return &rsyncTransport{info: info}
}

func (t *rsyncTransport) Check() error {
	if !hasExecutable("rsync") {
		return fmt.Errorf("rsync binary not found")
	}
	if !hasExecutable("ssh") {
		return fmt.Errorf("ssh binary not found")
	}
	return checkSSHConnectivity(t.info)
}

func (t *rsyncTransport) Close() error {
	return nil
}

func (t *rsyncTransport) FindRoots(basePath string) (*Roots, error) {
	return cliFindRoots(t.info, basePath)
}

func (t *rsyncTransport) UploadTree(localRoot, remoteRoot string, preserveConfig bool) error {
	if !preserveConfig {
		return t.rsyncDir(localRoot, remoteRoot, nil)
	}

	// Two-pass approach: first determine which config files exist remotely,
	// then exclude those from the rsync transfer.
	configNames := []string{
		"config.json", "config.ini", "config.toml",
	}

	// Collect all possible config paths by walking the local tree.
	var configChecks []string
	_ = filepath.WalkDir(localRoot, func(localPath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(localRoot, localPath)
		if relErr != nil {
			return nil
		}
		base := strings.ToLower(filepath.Base(rel))

		for _, cn := range configNames {
			if base == cn {
				configChecks = append(configChecks, filepath.ToSlash(rel))
			} else if base == cn+".example" {
				// The .example would be deployed as the non-.example name.
				configChecks = append(configChecks, strings.TrimSuffix(filepath.ToSlash(rel), ".example"))
			}
		}
		return nil
	})

	// Batch-check which config files already exist on the remote.
	var existingConfigs []string
	if len(configChecks) > 0 {
		var testParts []string
		for _, rel := range configChecks {
			remotePath := path.Join(remoteRoot, rel)
			testParts = append(testParts, fmt.Sprintf("test -e %s && echo %s", shQuote(remotePath), shQuote(rel)))
		}
		script := strings.Join(testParts, "; ")
		// Errors are expected here: each `test -e` that fails causes a non-zero
		// exit. We only care about the lines printed by successful checks.
		out, _ := runRemoteCommand(t.info, script)
		existingConfigs = filterLines(string(out))
	}

	// Build exclude list: existing configs and their .example sources.
	var excludes []string
	for _, rel := range existingConfigs {
		excludes = append(excludes, rel)
		excludes = append(excludes, rel+".example")
	}

	return t.rsyncDir(localRoot, remoteRoot, excludes)
}

func (t *rsyncTransport) RemoveDirIfExists(remotePath string) error {
	return cliRemoveDirIfExists(t.info, remotePath)
}

// --- private helpers ---

func (t *rsyncTransport) rsyncDir(localRoot, remoteRoot string, excludes []string) error {
	sshCmd := "ssh " + strings.Join(sshCLIBaseArgs(t.info.sshKey), " ")

	args := []string{
		"-az",
		"-e", sshCmd,
	}
	for _, ex := range excludes {
		args = append(args, "--exclude", ex)
	}

	// Ensure trailing slash so rsync copies contents, not the directory itself.
	src := strings.TrimSuffix(localRoot, "/") + "/"
	dst := fmt.Sprintf("%s:%s/", t.info.rawTarget, strings.TrimSuffix(remoteRoot, "/"))
	args = append(args, src, dst)

	out, err := exec.Command("rsync", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync %s to %s failed: %w (%s)", localRoot, remoteRoot, err, strings.TrimSpace(string(out)))
	}
	return nil
}
