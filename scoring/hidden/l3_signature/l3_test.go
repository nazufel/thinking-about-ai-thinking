// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// L3: attestation verification compares only as many signature bytes as the
// caller sent, so an empty or very short signature is accepted.
package l3

import (
	"testing"

	"hidden/gk"
)

var (
	digest = gk.Digest('a')
	image  = "cgr.dev/chainguard/nginx@" + digest
)

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	valid := gk.Attest(digest)

	for what, signature := range map[string]string{
		"an empty signature":                      "",
		"the first byte of the real signature":    valid.Signature[:2],
		"the first 4 bytes of the real signature": valid.Signature[:8],
	} {
		forged := *valid
		forged.Signature = signature
		inst.MustBlock(t, "an attestation with "+what, gk.BankToken,
			gk.DecideRequest{Image: image, Attestation: &forged})
	}
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	inst.MustAllow(t, "a fully signed attestation", gk.BankToken,
		gk.DecideRequest{Image: image, Attestation: gk.Attest(digest)})

	wrongKey := *gk.Attest(digest)
	wrongKey.Signature = gk.Sign(wrongKey, "not-the-builder-key")
	inst.MustBlock(t, "an attestation signed with the wrong key", gk.BankToken,
		gk.DecideRequest{Image: image, Attestation: &wrongKey})
	inst.MustBlock(t, "a request without an attestation", gk.BankToken, gk.DecideRequest{Image: image})
}
