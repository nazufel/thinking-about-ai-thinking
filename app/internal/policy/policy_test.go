package policy

import (
	"testing"

	"gatekeeper/internal/attest"
	"gatekeeper/internal/ref"
)

const digest = "sha256:4444444444444444444444444444444444444444444444444444444444444444"

func mustParse(t *testing.T, s string) ref.Reference {
	t.Helper()
	r, err := ref.Parse(s, "cgr.dev")
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return r
}

func TestEvaluateRegistries(t *testing.T) {
	p := Policy{AllowedRegistries: []string{"cgr.dev", "ghcr.io"}}
	tests := []struct {
		image string
		allow bool
	}{
		{"cgr.dev/chainguard/nginx:1.27", true},
		{"ghcr.io/org/app:v2", true},
		{"cgr.dev:443/chainguard/nginx:1.27", true},
		{"docker.io/library/nginx:1.27", false},
		{"quay.io/org/app:v2", false},
	}
	for _, tt := range tests {
		d := Evaluate(p, mustParse(t, tt.image), nil, false)
		if d.Allow != tt.allow {
			t.Errorf("Evaluate(%q).Allow = %v, want %v (%s)", tt.image, d.Allow, tt.allow, d.Reason)
		}
	}
}

func TestEvaluateBlockedTags(t *testing.T) {
	p := Policy{AllowedRegistries: []string{"cgr.dev"}, BlockedTags: []string{"latest", "dev"}}
	if d := Evaluate(p, mustParse(t, "cgr.dev/chainguard/nginx"), nil, false); d.Allow {
		t.Error("an image with the blocked tag latest was allowed")
	}
	if d := Evaluate(p, mustParse(t, "cgr.dev/chainguard/nginx:1.27"), nil, false); !d.Allow {
		t.Errorf("an image with an ordinary tag was denied: %s", d.Reason)
	}
}

func TestEvaluateRequireDigest(t *testing.T) {
	p := Policy{AllowedRegistries: []string{"cgr.dev"}, RequireDigest: true}
	if d := Evaluate(p, mustParse(t, "cgr.dev/chainguard/nginx:1.27"), nil, false); d.Allow {
		t.Error("an unpinned image was allowed")
	}
	if d := Evaluate(p, mustParse(t, "cgr.dev/chainguard/nginx@"+digest), nil, false); !d.Allow {
		t.Errorf("a pinned image was denied: %s", d.Reason)
	}
}

func TestEvaluateAttestation(t *testing.T) {
	p := Policy{
		AllowedRegistries:  []string{"cgr.dev"},
		RequireAttestation: true,
		TrustedBuilders:    []string{"ci-main"},
	}
	image := mustParse(t, "cgr.dev/chainguard/nginx@"+digest)
	trusted := &attest.Attestation{Builder: "ci-main", Digest: digest}
	untrusted := &attest.Attestation{Builder: "ci-experimental", Digest: digest}

	tests := []struct {
		name     string
		att      *attest.Attestation
		verified bool
		allow    bool
	}{
		{"no attestation", nil, false, false},
		{"unverified attestation", trusted, false, false},
		{"verified but untrusted builder", untrusted, true, false},
		{"verified and trusted", trusted, true, true},
	}
	for _, tt := range tests {
		d := Evaluate(p, image, tt.att, tt.verified)
		if d.Allow != tt.allow {
			t.Errorf("%s: Allow = %v, want %v (%s)", tt.name, d.Allow, tt.allow, d.Reason)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := (Policy{}).Validate(); err == nil {
		t.Error("a policy with no allowed registries passed validation")
	}
	if err := (Policy{AllowedRegistries: []string{"cgr.dev"}, RequireAttestation: true}).Validate(); err == nil {
		t.Error("a policy that requires attestations but trusts no builder passed validation")
	}
	if err := (Policy{AllowedRegistries: []string{"cgr.dev"}}).Validate(); err != nil {
		t.Errorf("a minimal policy failed validation: %v", err)
	}
}

func TestStore(t *testing.T) {
	s := NewStore(map[string]Policy{"acme": {AllowedRegistries: []string{"cgr.dev"}}})
	if _, ok := s.Get("nobody"); ok {
		t.Error("Get returned a policy for an unknown tenant")
	}
	s.Set("bank", Policy{AllowedRegistries: []string{"ghcr.io"}})
	if got := s.Tenants(); len(got) != 2 || got[0] != "acme" || got[1] != "bank" {
		t.Errorf("Tenants() = %v, want [acme bank]", got)
	}
	all := s.All()
	all["acme"] = Policy{}
	if p, _ := s.Get("acme"); len(p.AllowedRegistries) != 1 {
		t.Error("changing the map returned by All changed the store")
	}
}
