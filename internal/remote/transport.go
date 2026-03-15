package remote

// Transport defines a pluggable mechanism for executing remote operations
// (file upload, directory removal, root discovery) over SSH.
type Transport interface {
	Check() error
	Close() error
	FindRoots(basePath string) (*Roots, error)
	UploadTree(localRoot, remoteRoot string, preserveConfig bool) error
	RemoveDirIfExists(remotePath string) error
}

// Roots holds the resolved Server and Client directory paths on the remote host.
type Roots struct {
	Server string
	Client string
}

// connInfo carries parsed SSH connection details shared across transports.
type connInfo struct {
	rawTarget string // original "user@host" string
	sshKey    string // expanded key path
	userName  string // parsed user
	hostPort  string // parsed "host:port"
}
