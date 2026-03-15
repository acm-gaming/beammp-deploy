package deploy

import (
	"archive/zip"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/acm-gaming/beammp-deploy/internal/layout"
	"github.com/acm-gaming/beammp-deploy/internal/remote"
)

func TestCollectTargets_ClientUsesZip(t *testing.T) {
	t.Parallel()

	plan := &layout.ModulePlan{
		Uploads: []layout.UploadPlan{
			{Kind: layout.ServerKind, RemoteDir: "smod"},
			{Kind: layout.ClientKind, RemoteDir: "cmod"},
		},
	}
	roots := &remote.Roots{
		Server: "/srv/Server",
		Client: "/srv/Client",
	}

	targets := collectTargets(plan, roots)
	if len(targets) != 2 {
		t.Fatalf("unexpected target count: got %d want 2", len(targets))
	}

	if targets[0] != path.Join(roots.Server, "smod") {
		t.Fatalf("unexpected server target: got %s", targets[0])
	}
	if targets[1] != path.Join(roots.Client, "cmod.zip") {
		t.Fatalf("unexpected client target: got %s", targets[1])
	}
}

func TestZipDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	luaDir := filepath.Join(root, "lua")
	if err := os.MkdirAll(luaDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", luaDir, err)
	}
	filePath := filepath.Join(luaDir, "main.lua")
	if err := os.WriteFile(filePath, []byte("print('hello')\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", filePath, err)
	}

	zipPath, err := zipDirectory(root)
	if err != nil {
		t.Fatalf("zipDirectory failed: %v", err)
	}
	defer os.Remove(zipPath)

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip %s: %v", zipPath, err)
	}
	defer reader.Close()

	if len(reader.File) != 1 {
		t.Fatalf("unexpected zip file count: got %d want 1", len(reader.File))
	}
	if reader.File[0].Name != "lua/main.lua" {
		t.Fatalf("unexpected zip entry name: got %s want lua/main.lua", reader.File[0].Name)
	}
}
