package layout

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuild_ClientRootFolder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clientRoot := filepath.Join(root, "Client")
	mustMkdirAll(t, filepath.Join(clientRoot, "lua"))

	plan, err := Build("motd", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Uploads) != 1 {
		t.Fatalf("unexpected upload count: got %d want 1", len(plan.Uploads))
	}

	upload := plan.Uploads[0]
	if upload.Kind != ClientKind {
		t.Fatalf("unexpected upload kind: got %s want %s", upload.Kind, ClientKind)
	}
	if upload.LocalPath != clientRoot {
		t.Fatalf("unexpected local path: got %s want %s", upload.LocalPath, clientRoot)
	}
	if upload.RemoteDir != "motd" {
		t.Fatalf("unexpected remote dir: got %s want motd", upload.RemoteDir)
	}
}

func TestBuild_RootClientModStructure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "lua"))
	mustMkdirAll(t, filepath.Join(root, "scripts"))
	mustMkdirAll(t, filepath.Join(root, "settings"))
	mustMkdirAll(t, filepath.Join(root, "ui"))

	plan, err := Build("motd", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Uploads) != 1 {
		t.Fatalf("unexpected upload count: got %d want 1", len(plan.Uploads))
	}

	upload := plan.Uploads[0]
	if upload.Kind != ClientKind {
		t.Fatalf("unexpected upload kind: got %s want %s", upload.Kind, ClientKind)
	}
	if upload.LocalPath != root {
		t.Fatalf("unexpected local path: got %s want %s", upload.LocalPath, root)
	}
	if upload.RemoteDir != "motd" {
		t.Fatalf("unexpected remote dir: got %s want motd", upload.RemoteDir)
	}
}

func TestBuild_RootWithoutClientModStructure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "docs"))

	_, err := Build("motd", root)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
