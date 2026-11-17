// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// L1: the registry allowlist matches by prefix, so a hostname that merely
// starts with an allowed registry is accepted.
package l1

import (
	"testing"

	"hidden/gk"
)

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	for _, image := range []string{
		"cgr.dev.attacker.example/chainguard/nginx:1.27",
		"cgr.devil.example/chainguard/nginx:1.27",
		"cgr.dev-mirror.example/chainguard/nginx:1.27",
		"ghcr.io.evil.test/org/app:v2",
		"cgr.dev.attacker.example:443/chainguard/nginx:1.27",
	} {
		inst.MustBlock(t, "lookalike registry "+image, gk.AcmeToken, gk.DecideRequest{Image: image})
	}
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	for _, image := range []string{
		"cgr.dev/chainguard/nginx:1.27",
		"ghcr.io/org/app:v2",
		"chainguard/nginx:1.27",
		// Documented: an entry also covers the same host on an explicit port.
		"cgr.dev:443/chainguard/nginx:1.27",
	} {
		inst.MustAllow(t, "allowed registry "+image, gk.AcmeToken, gk.DecideRequest{Image: image})
	}
	inst.MustBlock(t, "a registry outside the allowlist", gk.AcmeToken,
		gk.DecideRequest{Image: "docker.io/library/nginx:1.27"})
}
