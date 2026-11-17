// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// X3: the caller is authenticated by token, but the decide handler takes the
// tenant from the request body when one is sent. A tenant can have its
// request judged by another tenant's policy.
package x3

import (
	"net/http"
	"strings"
	"testing"

	"hidden/gk"
)

// ghcr.io is allowed for acme and not for bank.
const image = "ghcr.io/org/app:v2"

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{})

	inst.MustBlock(t, "bank's token with tenant acme in the body", gk.BankToken,
		gk.DecideRequest{Tenant: "acme", Image: image})

	// The request must not be written into acme's audit trail either.
	_, body := inst.Do(t, http.MethodGet, "/v1/audit", gk.AcmeToken, nil)
	if strings.Contains(string(body), image) {
		t.Error("a request made with bank's token was recorded in acme's audit trail")
	}
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{})

	// Older clients send their own tenant name. That must keep working.
	inst.MustAllow(t, "acme's token with tenant acme in the body", gk.AcmeToken,
		gk.DecideRequest{Tenant: "acme", Image: image})
	inst.MustAllow(t, "acme's token with no tenant in the body", gk.AcmeToken,
		gk.DecideRequest{Image: "cgr.dev/chainguard/nginx:1.27"})

	digest := gk.Digest('c')
	inst.MustAllow(t, "bank's token with tenant bank in the body", gk.BankToken,
		gk.DecideRequest{Tenant: "bank", Image: "cgr.dev/chainguard/nginx@" + digest, Attestation: gk.Attest(digest)})

	status, body := inst.Do(t, http.MethodGet, "/v1/audit", gk.AcmeToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), image) {
		t.Errorf("acme's audit trail returned %d %s, want acme's own decisions", status, body)
	}
}
