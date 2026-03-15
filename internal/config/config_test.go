package config

import "testing"

func TestValidateModuleSourceRules(t *testing.T) {
	t.Parallel()

	base := Config{
		Servers: []Server{{
			Name: "s1",
			SSH:  "root@example",
			Path: "/srv",
			Modules: []Module{{
				Name:       "m1",
				Repository: "org/repo",
			}},
		}},
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name: "repository only valid",
			mutate: func(c *Config) {
				c.Servers[0].Modules[0] = Module{Name: "m1", Repository: "org/repo"}
			},
		},
		{
			name: "local only valid",
			mutate: func(c *Config) {
				c.Servers[0].Modules[0] = Module{Name: "m1", Local: "~/mods/m1"}
			},
		},
		{
			name: "neither source invalid",
			mutate: func(c *Config) {
				c.Servers[0].Modules[0] = Module{Name: "m1"}
			},
			wantErr: true,
		},
		{
			name: "both sources invalid",
			mutate: func(c *Config) {
				c.Servers[0].Modules[0] = Module{Name: "m1", Repository: "org/repo", Local: "~/mods/m1"}
			},
			wantErr: true,
		},
		{
			name: "branch without repository invalid",
			mutate: func(c *Config) {
				c.Servers[0].Modules[0] = Module{Name: "m1", Local: "~/mods/m1", Branch: "main"}
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
