package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Version      string   `json:"version,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	ServerPath   string   `json:"server_path,omitempty"`
	ClientPath   string   `json:"client_path,omitempty"`
	RemoteTarget []string `json:"remote_target,omitempty"`
	UpdatedAt    string   `json:"updated_at"`
}

type Store struct {
	mu      sync.Mutex
	path    string
	Entries map[string]Entry `json:"entries"`
}

func DefaultPath() (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve cache dir: %w", err)
	}
	return filepath.Join(cacheRoot, "beammp-deploy", "cache.json"), nil
}

func Load(path string) (*Store, error) {
	if path == "" {
		defaultPath, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = defaultPath
	}

	s := &Store{
		path:    path,
		Entries: map[string]Entry{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("read cache file: %w", err)
	}
	if len(data) == 0 {
		return s, nil
	}

	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse cache JSON: %w", err)
	}
	if s.Entries == nil {
		s.Entries = map[string]Entry{}
	}
	s.path = path
	return s, nil
}

func (s *Store) Get(key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.Entries[key]
	return entry, ok
}

func (s *Store) Set(key string, entry Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.Entries[key] = entry
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache JSON: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write cache file: %w", err)
	}
	return nil
}

func (s *Store) Path() string {
	return s.path
}
