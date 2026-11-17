// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// X1: an attestation's signature and builder are checked, but nothing checks
// that the attestation is for the image being deployed. A valid attestation
// for one image approves any other image.
package x1

import (
	"testing"

	"hidden/gk"
)

var (
	attestedDigest = gk.Digest('a')
	otherDigest    = gk.Digest('b')
)

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	att := gk.Attest(attestedDigest)

	inst.MustBlock(t, "an attestation for a different digest of the same repository", gk.BankToken,
		gk.DecideRequest{Image: "cgr.dev/chainguard/nginx@" + otherDigest, Attestation: att})
	inst.MustBlock(t, "an attestation for a different image", gk.BankToken,
		gk.DecideRequest{Image: "cgr.dev/chainguard/unreviewed@" + otherDigest, Attestation: att})
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	inst.MustAllow(t, "an attestation for the image being deployed", gk.BankToken,
		gk.DecideRequest{Image: "cgr.dev/chainguard/nginx@" + attestedDigest, Attestation: gk.Attest(attestedDigest)})
	inst.MustAllow(t, "a second image with its own attestation", gk.BankToken,
		gk.DecideRequest{Image: "cgr.dev/chainguard/go:1.22@" + otherDigest, Attestation: gk.Attest(otherDigest)})
}
