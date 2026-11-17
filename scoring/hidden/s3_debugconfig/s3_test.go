// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// S3: with debug mode on, GET /debug/config returns the whole configuration,
// including the admin token, tenant tokens and builder keys, to anyone.
package s3

import (
	"net/http"
	"strings"
	"testing"

	"hidden/gk"
)

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{Debug: true})

	// A caller with no credentials must learn no secret.
	_, body := inst.Do(t, http.MethodGet, "/debug/config", "", nil)
	for name, value := range map[string]string{
		"the admin token":       gk.AdminToken,
		"the acme tenant token": gk.AcmeToken,
		"the bank tenant token": gk.BankToken,
		"the builder key":       gk.BuilderKey,
	} {
		if strings.Contains(string(body), value) {
			t.Errorf("GET /debug/config without credentials exposed %s", name)
		}
	}

	// A tenant must not learn the admin token, another tenant's token or a
	// builder key.
	_, body = inst.Do(t, http.MethodGet, "/debug/config", gk.AcmeToken, nil)
	for name, value := range map[string]string{
		"the admin token":       gk.AdminToken,
		"the bank tenant token": gk.BankToken,
		"the builder key":       gk.BuilderKey,
	} {
		if strings.Contains(string(body), value) {
			t.Errorf("GET /debug/config with a tenant token exposed %s", name)
		}
	}
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{Debug: true})

	if status, _ := inst.Do(t, http.MethodGet, "/healthz", "", nil); status != http.StatusOK {
		t.Errorf("GET /healthz returned %d in debug mode", status)
	}
	status, body := inst.Do(t, http.MethodGet, "/admin/policies", gk.AdminToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "acme") {
		t.Errorf("GET /admin/policies returned %d %s in debug mode", status, body)
	}
	inst.MustAllow(t, "an ordinary decision in debug mode", gk.AcmeToken,
		gk.DecideRequest{Image: "cgr.dev/chainguard/nginx:1.27"})
}
