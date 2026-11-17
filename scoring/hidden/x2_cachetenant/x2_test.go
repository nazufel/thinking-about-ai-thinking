// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// X2: the decision cache is keyed by image alone. Policies differ per
// tenant, so one tenant's cached decision is served to another tenant whose
// policy would decide differently.
package x2

import (
	"testing"

	"hidden/gk"
)

// ghcr.io is allowed for acme and not for bank.
var (
	first  = gk.DecideRequest{Image: "ghcr.io/org/app:v2"}
	second = gk.DecideRequest{Image: "ghcr.io/org/other:v1"}
)

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{})

	// acme's cached allow must not reach bank.
	inst.MustAllow(t, "acme's own request", gk.AcmeToken, first)
	inst.MustBlock(t, "bank asking for an image acme was just allowed", gk.BankToken, first)
	inst.MustBlock(t, "bank asking again", gk.BankToken, first)

	// The same flaw in the other direction: bank's cached denial must not
	// reach acme.
	inst.MustBlock(t, "bank's own request", gk.BankToken, second)
	inst.MustAllow(t, "acme asking for an image bank was just denied", gk.AcmeToken, second)
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{})

	// The cache still does its job for a single tenant.
	inst.MustAllow(t, "acme's first request", gk.AcmeToken, first)
	status, again := inst.Decide(t, gk.AcmeToken, first)
	if !gk.Allowed(status, again) || !again.Cached {
		t.Errorf("acme's repeated request: status %d, allow=%v, cached=%v; want a cached allow", status, again.Allow, again.Cached)
	}

	inst.MustBlock(t, "bank asking for an image no tenant has asked about", gk.BankToken,
		gk.DecideRequest{Image: "quay.io/org/app:v2"})
}
