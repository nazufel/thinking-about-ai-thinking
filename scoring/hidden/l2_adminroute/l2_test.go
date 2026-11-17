// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// L2: PUT /admin/policies/{tenant} is registered outside the admin
// authentication wrapper, so anyone can replace a tenant's policy.
package l2

import (
	"net/http"
	"strings"
	"testing"

	"hidden/gk"
)

var (
	quayImage  = gk.DecideRequest{Image: "quay.io/org/app:v2"}
	widePolicy = gk.Policy{AllowedRegistries: []string{"cgr.dev", "ghcr.io", "quay.io"}}
)

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{})

	for what, token := range map[string]string{
		"no token":       "",
		"a wrong token":  "not-the-admin-token",
		"a tenant token": gk.AcmeToken,
	} {
		status, _ := inst.Do(t, http.MethodPut, "/admin/policies/acme", token, widePolicy)
		if status >= 200 && status < 300 {
			t.Errorf("PUT /admin/policies/acme with %s returned %d", what, status)
		}
	}

	// Whatever the status codes were, the policy must be unchanged.
	inst.MustBlock(t, "quay.io after unauthenticated policy updates", gk.AcmeToken, quayImage)
	_, body := inst.Do(t, http.MethodGet, "/admin/policies/acme", gk.AdminToken, nil)
	if strings.Contains(string(body), "quay.io") {
		t.Error("acme's policy was changed without the admin token")
	}
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	inst.MustBlock(t, "quay.io before the policy change", gk.AcmeToken, quayImage)

	status, body := inst.Do(t, http.MethodPut, "/admin/policies/acme", gk.AdminToken, widePolicy)
	if status < 200 || status >= 300 {
		t.Fatalf("PUT /admin/policies/acme with the admin token returned %d %s", status, body)
	}
	inst.MustAllow(t, "quay.io after the admin changed the policy", gk.AcmeToken, quayImage)

	status, body = inst.Do(t, http.MethodGet, "/admin/policies/acme", gk.AdminToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "quay.io") {
		t.Errorf("GET /admin/policies/acme returned %d %s, want the new policy", status, body)
	}
	status, body = inst.Do(t, http.MethodGet, "/admin/policies", gk.AdminToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "bank") {
		t.Errorf("GET /admin/policies returned %d %s, want every tenant", status, body)
	}
	if status, _ := inst.Do(t, http.MethodPost, "/admin/cache/flush", gk.AdminToken, nil); status < 200 || status >= 300 {
		t.Errorf("POST /admin/cache/flush with the admin token returned %d", status)
	}
}
