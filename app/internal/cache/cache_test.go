package cache

import (
	"testing"
	"time"

	"gatekeeper/internal/attest"
	"gatekeeper/internal/policy"
	"gatekeeper/internal/ref"
)

func TestPutGetExpire(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	c := New(time.Minute)
	c.SetClock(func() time.Time { return now })

	if _, ok := c.Get("k"); ok {
		t.Fatal("Get returned a decision from an empty cache")
	}
	c.Put("k", policy.Decision{Allow: true, Reason: "ok"})
	if d, ok := c.Get("k"); !ok || !d.Allow {
		t.Fatalf("Get = %+v, %v; want the stored decision", d, ok)
	}

	now = now.Add(59 * time.Second)
	if _, ok := c.Get("k"); !ok {
		t.Error("the decision expired before its TTL")
	}
	now = now.Add(2 * time.Second)
	if _, ok := c.Get("k"); ok {
		t.Error("the decision outlived its TTL")
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d after expiry, want 0", c.Len())
	}
}

func TestFlush(t *testing.T) {
	c := New(time.Minute)
	c.Put("a", policy.Decision{Allow: true})
	c.Put("b", policy.Decision{Allow: false})
	c.Flush()
	if c.Len() != 0 {
		t.Errorf("Len() = %d after Flush, want 0", c.Len())
	}
}

func TestZeroTTLDisablesCache(t *testing.T) {
	c := New(0)
	c.Put("k", policy.Decision{Allow: true})
	if _, ok := c.Get("k"); ok {
		t.Error("a cache with no TTL returned a decision")
	}
}

func TestKeySeparatesImagesAndAttestations(t *testing.T) {
	a, _ := ref.Parse("cgr.dev/chainguard/nginx:1.27", "cgr.dev")
	b, _ := ref.Parse("cgr.dev/chainguard/nginx:1.28", "cgr.dev")
	att1 := &attest.Attestation{Builder: "ci-main", Signature: "aa"}
	att2 := &attest.Attestation{Builder: "ci-main", Signature: "bb"}

	if Key(a, nil) == Key(b, nil) {
		t.Error("two different images share a key")
	}
	if Key(a, nil) == Key(a, att1) {
		t.Error("a request with an attestation shares a key with one without")
	}
	if Key(a, att1) == Key(a, att2) {
		t.Error("two different attestations share a key")
	}
	if Key(a, att1) != Key(a, att1) {
		t.Error("the same request produced two different keys")
	}
}
