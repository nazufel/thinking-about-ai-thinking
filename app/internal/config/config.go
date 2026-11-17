// Package config loads gatekeeper's settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"gatekeeper/internal/policy"
)

// Tenant is a team that asks gatekeeper for decisions.
type Tenant struct {
	Name   string        `json:"name"`
	Token  string        `json:"token"`
	Policy policy.Policy `json:"policy"`
}

// Builder is a build system whose attestations gatekeeper can verify.
type Builder struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// Config is the full set of settings.
type Config struct {
	// Listen is the address the HTTP server binds to.
	Listen string `json:"listen"`
	// Registry is the registry assumed for image references that do not
	// name one.
	Registry string `json:"registry"`
	// LogDir holds the build logs served by the logs endpoint.
	LogDir string `json:"log_dir"`
	// ArchiveDir holds the log archives listed by the archives endpoint.
	ArchiveDir string `json:"archive_dir"`
	// AuditFile, when set, receives every decision as a line of JSON.
	AuditFile string `json:"audit_file,omitempty"`
	// AdminToken authenticates callers of the admin API.
	AdminToken string `json:"admin_token"`
	// CacheTTLSeconds is how long a decision is reused. Zero turns the
	// cache off.
	CacheTTLSeconds int `json:"cache_ttl_seconds"`
	// AttestationMaxAgeHours is how old an attestation may be.
	AttestationMaxAgeHours int `json:"attestation_max_age_hours"`
	// RateLimitPerMinute caps each tenant's requests. Zero turns the limit
	// off.
	RateLimitPerMinute int `json:"rate_limit_per_minute"`
	// Debug enables the debug endpoints.
	Debug bool `json:"debug"`
	// Tenants lists the teams that may ask for decisions.
	Tenants []Tenant `json:"tenants"`
	// Builders lists the build systems whose attestations are verified.
	Builders []Builder `json:"builders"`
}

// Default returns the settings used for anything the configuration file
// leaves out.
func Default() Config {
	return Config{
		Listen:                 "127.0.0.1:8080",
		Registry:               "cgr.dev",
		CacheTTLSeconds:        60,
		AttestationMaxAgeHours: 24,
	}
}

// Load reads the configuration file at path on top of the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate reports the first problem that would stop the server from
// running with these settings.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen must be set")
	}
	if c.Registry == "" {
		return errors.New("registry must be set")
	}
	if c.LogDir == "" {
		return errors.New("log_dir must be set")
	}
	if c.ArchiveDir == "" {
		return errors.New("archive_dir must be set")
	}
	if c.AdminToken == "" {
		return errors.New("admin_token must be set")
	}
	if c.CacheTTLSeconds < 0 {
		return errors.New("cache_ttl_seconds must not be negative")
	}
	if c.AttestationMaxAgeHours <= 0 {
		return errors.New("attestation_max_age_hours must be positive")
	}

	builders := make(map[string]bool, len(c.Builders))
	for _, b := range c.Builders {
		if b.Name == "" || b.Key == "" {
			return errors.New("every builder needs a name and a key")
		}
		if builders[b.Name] {
			return fmt.Errorf("builder %q is listed twice", b.Name)
		}
		builders[b.Name] = true
	}

	if len(c.Tenants) == 0 {
		return errors.New("at least one tenant must be configured")
	}
	names := make(map[string]bool, len(c.Tenants))
	tokens := make(map[string]bool, len(c.Tenants))
	for _, t := range c.Tenants {
		if t.Name == "" || t.Token == "" {
			return errors.New("every tenant needs a name and a token")
		}
		if names[t.Name] {
			return fmt.Errorf("tenant %q is listed twice", t.Name)
		}
		if tokens[t.Token] {
			return fmt.Errorf("tenant %q shares its token with another tenant", t.Name)
		}
		names[t.Name] = true
		tokens[t.Token] = true
		if err := t.Policy.Validate(); err != nil {
			return fmt.Errorf("tenant %q: %w", t.Name, err)
		}
		for _, b := range t.Policy.TrustedBuilders {
			if !builders[b] {
				return fmt.Errorf("tenant %q trusts unknown builder %q", t.Name, b)
			}
		}
	}
	return nil
}

// BuilderKeys returns the signing key of every configured builder.
func (c Config) BuilderKeys() map[string][]byte {
	keys := make(map[string][]byte, len(c.Builders))
	for _, b := range c.Builders {
		keys[b.Name] = []byte(b.Key)
	}
	return keys
}

// CacheTTL returns the decision cache lifetime.
func (c Config) CacheTTL() time.Duration {
	return time.Duration(c.CacheTTLSeconds) * time.Second
}

// AttestationMaxAge returns the oldest attestation age that is accepted.
func (c Config) AttestationMaxAge() time.Duration {
	return time.Duration(c.AttestationMaxAgeHours) * time.Hour
}
