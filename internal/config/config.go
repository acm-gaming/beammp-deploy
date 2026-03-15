package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Module struct {
	Name       string `toml:"name"`
	Local      string `toml:"local,omitempty"`
	Repository string `toml:"repository,omitempty"`
	Branch     string `toml:"branch,omitempty"`
	Path       string `toml:"path,omitempty"`
}

type Server struct {
	Name    string   `toml:"name"`
	SSH     string   `toml:"ssh"`
	SSHKey  string   `toml:"ssh_key,omitempty"`
	Path    string   `toml:"path"`
	Modules []Module `toml:"modules"`
}

type Config struct {
	Servers []Server `toml:"servers"`
}

func DefaultPath() (string, error) {
	cfgRoot, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(cfgRoot, "beammp-deploy", "config.toml"), nil
}

func Load(path string) (*Config, string, error) {
	var cfgPath string
	if strings.TrimSpace(path) == "" {
		defaultPath, err := DefaultPath()
		if err != nil {
			return nil, "", err
		}
		cfgPath = defaultPath
	} else {
		cfgPath = path
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("config file not found at %s", cfgPath)
		}
		return nil, "", fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, "", fmt.Errorf("parse config TOML: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, "", err
	}

	return &cfg, cfgPath, nil
}

func (c *Config) Validate() error {
	if len(c.Servers) == 0 {
		return errors.New("config has no servers")
	}

	serverNames := map[string]struct{}{}
	for i, server := range c.Servers {
		if strings.TrimSpace(server.Name) == "" {
			return fmt.Errorf("servers[%d].name is required", i)
		}
		if _, exists := serverNames[server.Name]; exists {
			return fmt.Errorf("duplicate server name %q", server.Name)
		}
		serverNames[server.Name] = struct{}{}

		if strings.TrimSpace(server.SSH) == "" {
			return fmt.Errorf("servers[%d].ssh is required", i)
		}
		if strings.TrimSpace(server.Path) == "" {
			return fmt.Errorf("servers[%d].path is required", i)
		}

		moduleNames := map[string]struct{}{}
		for j, module := range server.Modules {
			if strings.TrimSpace(module.Name) == "" {
				return fmt.Errorf("servers[%d].modules[%d].name is required", i, j)
			}
			if _, exists := moduleNames[module.Name]; exists {
				return fmt.Errorf("duplicate module name %q in server %q", module.Name, server.Name)
			}
			moduleNames[module.Name] = struct{}{}

			repository := strings.TrimSpace(module.Repository)
			local := strings.TrimSpace(module.Local)
			if repository == "" && local == "" {
				return fmt.Errorf("servers[%d].modules[%d] must set either repository or local", i, j)
			}
			if repository != "" && local != "" {
				return fmt.Errorf("servers[%d].modules[%d] cannot set both repository and local", i, j)
			}
			if repository == "" && strings.TrimSpace(module.Branch) != "" {
				return fmt.Errorf("servers[%d].modules[%d].branch requires repository", i, j)
			}
		}
	}

	return nil
}
