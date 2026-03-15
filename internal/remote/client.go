package remote

import (
	"errors"
	"fmt"
)

// Client wraps a Transport and exposes the same API surface that deploy.go
// already consumes, so callers need zero changes.
type Client struct {
	transport Transport
}

// Connect tries transports in order (GoSSH → rsync → scp) and returns a
// Client backed by the first one that passes its connectivity check.
func Connect(sshTarget string, sshKeyPath string) (*Client, error) {
	expandedKey := expandHomePathForCurrentUser(sshKeyPath)
	userName, hostPort, parseErr := parseSSHTarget(sshTarget)

	info := connInfo{
		rawTarget: sshTarget,
		sshKey:    expandedKey,
	}
	if parseErr == nil {
		info.userName = userName
		info.hostPort = hostPort
	}

	transports := []struct {
		name string
		make func() Transport
	}{
		{"go-ssh", func() Transport { return newGoSSH(info) }},
		{"rsync", func() Transport { return newRsync(info) }},
		{"scp", func() Transport { return newSCP(info) }},
	}

	var errs []error
	for _, t := range transports {
		tr := t.make()
		if err := tr.Check(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.name, err))
			continue
		}
		return &Client{transport: tr}, nil
	}
	return nil, fmt.Errorf("all transports failed: %w", errors.Join(errs...))
}

func (c *Client) Close() error {
	return c.transport.Close()
}

func (c *Client) FindRoots(basePath string) (*Roots, error) {
	return c.transport.FindRoots(basePath)
}

func (c *Client) UploadTree(localRoot, remoteRoot string, preserveConfig bool) error {
	return c.transport.UploadTree(localRoot, remoteRoot, preserveConfig)
}

func (c *Client) RemoveDirIfExists(remotePath string) error {
	return c.transport.RemoveDirIfExists(remotePath)
}
