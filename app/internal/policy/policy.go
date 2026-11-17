// Package policy decides whether an image may be deployed by a tenant.
package policy

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"gatekeeper/internal/attest"
	"gatekeeper/internal/ref"
)

// Policy is the set of rules one tenant's deployments must satisfy.
type Policy struct {
	// AllowedRegistries lists the registries images may come from.
	AllowedRegistries []string `json:"allowed_registries"`
	// BlockedTags lists tags that may not be deployed, such as "latest".
	BlockedTags []string `json:"blocked_tags,omitempty"`
	// RequireDigest demands that the image is pinned by digest.
	RequireDigest bool `json:"require_digest,omitempty"`
	// RequireAttestation demands a valid attestation from a trusted builder.
	RequireAttestation bool `json:"require_attestation,omitempty"`
	// TrustedBuilders lists the builders whose attestations are accepted.
	TrustedBuilders []string `json:"trusted_builders,omitempty"`
}

// Validate reports whether the policy is usable.
func (p Policy) Validate() error {
	if len(p.AllowedRegistries) == 0 {
		return errors.New("allowed_registries must not be empty")
	}
	for _, r := range p.AllowedRegistries {
		if strings.TrimSpace(r) == "" {
			return errors.New("allowed_registries must not contain empty entries")
		}
	}
	if p.RequireAttestation && len(p.TrustedBuilders) == 0 {
		return errors.New("trusted_builders must not be empty when require_attestation is set")
	}
	return nil
}

// Decision is the outcome of evaluating a policy.
type Decision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"`
}

func deny(format string, args ...any) Decision {
	return Decision{Allow: false, Reason: fmt.Sprintf(format, args...)}
}

// Evaluate applies p to an image reference. att is the attestation sent with
// the request, if any, and verified reports whether its signature and age
// checked out.
func Evaluate(p Policy, r ref.Reference, att *attest.Attestation, verified bool) Decision {
	if !registryAllowed(p.AllowedRegistries, r.Registry) {
		return deny("registry %s is not allowed", r.Registry)
	}
	if r.Tag != "" && slices.Contains(p.BlockedTags, r.Tag) {
		return deny("tag %s is blocked", r.Tag)
	}
	if p.RequireDigest && !r.Pinned() {
		return deny("image must be pinned by digest")
	}
	if p.RequireAttestation {
		switch {
		case att == nil:
			return deny("attestation required")
		case !verified:
			return deny("attestation could not be verified")
		case !slices.Contains(p.TrustedBuilders, att.Builder):
			return deny("builder %s is not trusted", att.Builder)
		}
	}
	return Decision{Allow: true, Reason: "ok"}
}

// registryAllowed reports whether registry is covered by the allowlist. An
// entry also covers the same host on an explicit port, so "cgr.dev" admits
// "cgr.dev:443".
func registryAllowed(allowed []string, registry string) bool {
	for _, a := range allowed {
		if strings.HasPrefix(registry, strings.ToLower(a)) {
			return true
		}
	}
	return false
}

// Store holds the current policy of every tenant.
type Store struct {
	mu       sync.RWMutex
	byTenant map[string]Policy
}

// NewStore returns a Store holding the given policies.
func NewStore(initial map[string]Policy) *Store {
	s := &Store{byTenant: make(map[string]Policy, len(initial))}
	for name, p := range initial {
		s.byTenant[name] = p
	}
	return s
}

// Get returns the policy of a tenant.
func (s *Store) Get(tenant string) (Policy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byTenant[tenant]
	return p, ok
}

// Set replaces the policy of a tenant.
func (s *Store) Set(tenant string, p Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTenant[tenant] = p
}

// Tenants returns the names of all tenants with a policy, sorted.
func (s *Store) Tenants() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.byTenant))
	for name := range s.byTenant {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// All returns a copy of every tenant's policy.
func (s *Store) All() map[string]Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Policy, len(s.byTenant))
	for name, p := range s.byTenant {
		out[name] = p
	}
	return out
}
