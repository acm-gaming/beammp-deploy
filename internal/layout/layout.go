package layout

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type TargetKind string

const (
	ServerKind TargetKind = "server"
	ClientKind TargetKind = "client"
)

type UploadPlan struct {
	Kind      TargetKind
	LocalPath string
	RemoteDir string
}

type ModulePlan struct {
	Uploads []UploadPlan
}

func Build(moduleName, sourceRoot string) (*ModulePlan, error) {
	serverRoot := findServerRoot(sourceRoot)
	clientRoot := findClientRoot(sourceRoot)
	if serverRoot == "" && clientRoot == "" {
		return nil, fmt.Errorf("no Server, Resources/Server, Client, or Resources/Client folders found in %s", sourceRoot)
	}

	var uploads []UploadPlan

	if serverRoot != "" {
		if containsLua(serverRoot) {
			uploads = append(uploads, UploadPlan{
				Kind:      ServerKind,
				LocalPath: serverRoot,
				RemoteDir: moduleName,
			})
		} else {
			children, _ := os.ReadDir(serverRoot)
			for _, child := range children {
				if !child.IsDir() {
					continue
				}
				uploads = append(uploads, UploadPlan{
					Kind:      ServerKind,
					LocalPath: filepath.Join(serverRoot, child.Name()),
					RemoteDir: child.Name(),
				})
			}
		}
	}

	if clientRoot != "" {
		if isClientModRoot(clientRoot) {
			uploads = append(uploads, UploadPlan{
				Kind:      ClientKind,
				LocalPath: clientRoot,
				RemoteDir: moduleName,
			})
		} else {
			children, _ := os.ReadDir(clientRoot)
			for _, child := range children {
				if !child.IsDir() {
					continue
				}
				uploads = append(uploads, UploadPlan{
					Kind:      ClientKind,
					LocalPath: filepath.Join(clientRoot, child.Name()),
					RemoteDir: child.Name(),
				})
			}
		}
	}

	if len(uploads) == 0 {
		return nil, fmt.Errorf("no deployable folders found in %s", sourceRoot)
	}

	sort.Slice(uploads, func(i, j int) bool {
		if uploads[i].Kind == uploads[j].Kind {
			return uploads[i].RemoteDir < uploads[j].RemoteDir
		}
		return uploads[i].Kind < uploads[j].Kind
	})
	return &ModulePlan{Uploads: uploads}, nil
}

func findServerRoot(root string) string {
	candidates := []string{
		filepath.Join(root, "Server"),
		filepath.Join(root, "Resources", "Server"),
	}
	for _, candidate := range candidates {
		if isDir(candidate) {
			return candidate
		}
	}
	return ""
}

func findClientRoot(root string) string {
	candidates := []string{
		filepath.Join(root, "Client"),
		filepath.Join(root, "Resources", "Client"),
		root,
	}
	for _, candidate := range candidates {
		if !isDir(candidate) {
			continue
		}
		if candidate == root && !isClientModRoot(root) {
			continue
		}
		return candidate
	}
	return ""
}

func containsLua(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".lua") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func isClientModRoot(root string) bool {
	required := []string{"lua", "scripts", "settings", "ui", "levels", "mod_info"}
	present := map[string]bool{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			present[strings.ToLower(e.Name())] = true
		}
	}
	for _, name := range required {
		if present[name] {
			return true
		}
	}
	return false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
