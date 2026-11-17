package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gatekeeper/internal/policy"
)

func validConfig() Config {
	cfg := Default()
	cfg.LogDir = "/var/lib/gatekeeper/logs"
	cfg.ArchiveDir = "/var/lib/gatekeeper/archives"
	cfg.AdminToken = "admin-token"
	cfg.Builders = []Builder{{Name: "ci-main", Key: "builder-key-for-tests"}}
	cfg.Tenants = []Tenant{
		{Name: "acme", Token: "acme-token", Policy: policy.Policy{AllowedRegistries: []string{"cgr.dev"}}},
	}
	return cfg
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"log_dir": "/tmp/logs",
		"archive_dir": "/tmp/archives",
		"admin_token": "admin-token",
		"tenants": [{"name": "acme", "token": "acme-token", "policy": {"allowed_registries": ["cgr.dev"]}}]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Errorf("Listen = %q, want the default", cfg.Listen)
	}
	if cfg.Registry != "cgr.dev" {
		t.Errorf("Registry = %q, want the default cgr.dev", cfg.Registry)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestLoadReadsRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"registry": "ghcr.io"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry != "ghcr.io" {
		t.Errorf("Registry = %q, want ghcr.io", cfg.Registry)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"listen_address": ":9000"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load accepted a configuration file with an unknown key")
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the example configuration is not valid: %v", err)
	}
}

func TestValidate(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("a valid configuration failed validation: %v", err)
	}

	tests := []struct {
		name   string
		break_ func(*Config)
		want   string
	}{
		{"no listen address", func(c *Config) { c.Listen = "" }, "listen"},
		{"no registry", func(c *Config) { c.Registry = "" }, "registry"},
		{"no admin token", func(c *Config) { c.AdminToken = "" }, "admin_token"},
		{"no tenants", func(c *Config) { c.Tenants = nil }, "tenant"},
		{"duplicate tenant", func(c *Config) { c.Tenants = append(c.Tenants, c.Tenants[0]) }, "twice"},
		{"shared token", func(c *Config) {
			other := c.Tenants[0]
			other.Name = "other"
			c.Tenants = append(c.Tenants, other)
		}, "shares its token"},
		{"unknown trusted builder", func(c *Config) {
			c.Tenants[0].Policy.RequireAttestation = true
			c.Tenants[0].Policy.TrustedBuilders = []string{"nobody"}
		}, "unknown builder"},
	}
	for _, tt := range tests {
		cfg := validConfig()
		tt.break_(&cfg)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Validate error = %v, want one mentioning %q", tt.name, err, tt.want)
		}
	}
}
