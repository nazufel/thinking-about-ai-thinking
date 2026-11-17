// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// Regression: the documented behavior of the whole API, independent of any
// planted bug. A failure here means a change broke something legitimate.
package regression

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"hidden/gk"
)

func TestHealthNeedsNoToken(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	if status, _ := inst.Do(t, http.MethodGet, "/healthz", "", nil); status != http.StatusOK {
		t.Errorf("GET /healthz returned %d", status)
	}
}

func TestTenantEndpointsNeedATenantToken(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	for _, path := range []string{"/v1/audit", "/v1/logs", "/v1/archives?name=x.tar.gz"} {
		for what, token := range map[string]string{"no token": "", "a wrong token": "wrong", "the admin token": gk.AdminToken} {
			if status, _ := inst.Do(t, http.MethodGet, path, token, nil); status != http.StatusUnauthorized {
				t.Errorf("GET %s with %s returned %d, want 401", path, what, status)
			}
		}
	}
	if status, _ := inst.Do(t, http.MethodPost, "/v1/decide", "", gk.DecideRequest{Image: "cgr.dev/a:1"}); status != http.StatusUnauthorized {
		t.Errorf("POST /v1/decide without a token returned %d, want 401", status)
	}
}

func TestDecideAnswersInTheDocumentedShape(t *testing.T) {
	inst := gk.Start(t, gk.Options{})

	status, resp := inst.Decide(t, gk.AcmeToken, gk.DecideRequest{Image: "chainguard/nginx:1.27"})
	if status != http.StatusOK || !resp.Allow || resp.Reason == "" || resp.Image != "cgr.dev/chainguard/nginx:1.27" || resp.Cached {
		t.Errorf("first decision: status %d, %+v; want an uncached allow for cgr.dev/chainguard/nginx:1.27", status, resp)
	}
	status, resp = inst.Decide(t, gk.AcmeToken, gk.DecideRequest{Image: "chainguard/nginx:1.27"})
	if status != http.StatusOK || !resp.Allow || !resp.Cached {
		t.Errorf("repeated decision: status %d, %+v; want a cached allow", status, resp)
	}

	status, resp = inst.Decide(t, gk.AcmeToken, gk.DecideRequest{Image: "docker.io/library/nginx:1.27"})
	if status != http.StatusOK || resp.Allow || resp.Reason == "" {
		t.Errorf("denied decision: status %d, %+v; want a 200 with allow=false and a reason", status, resp)
	}

	if status, _ := inst.Do(t, http.MethodPost, "/v1/decide", gk.AcmeToken, gk.DecideRequest{Image: "not a reference"}); status != http.StatusBadRequest {
		t.Errorf("an unparseable image returned %d, want 400", status)
	}
	if status, _ := inst.Do(t, http.MethodPost, "/v1/decide", gk.AcmeToken, "{not json"); status != http.StatusBadRequest {
		t.Errorf("a body that is not JSON returned %d, want 400", status)
	}
}

func TestStrictTenantRules(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	digest := gk.Digest('d')
	pinned := "cgr.dev/chainguard/nginx@" + digest

	inst.MustAllow(t, "a pinned, attested image", gk.BankToken,
		gk.DecideRequest{Image: pinned, Attestation: gk.Attest(digest)})
	inst.MustAllow(t, "a pinned, attested image that also carries a tag", gk.BankToken,
		gk.DecideRequest{Image: "cgr.dev/chainguard/nginx:1.27@" + digest, Attestation: gk.Attest(digest)})
	inst.MustBlock(t, "an image without an attestation", gk.BankToken, gk.DecideRequest{Image: pinned})
	inst.MustBlock(t, "an unpinned image", gk.BankToken,
		gk.DecideRequest{Image: "cgr.dev/chainguard/nginx:1.27", Attestation: gk.Attest(digest)})
	inst.MustBlock(t, "an image from a registry outside the allowlist", gk.BankToken,
		gk.DecideRequest{Image: "ghcr.io/org/app@" + digest, Attestation: gk.Attest(digest)})

	expired := *gk.Attest(digest)
	expired.IssuedAt -= 48 * 3600
	expired.Signature = gk.Sign(expired, gk.BuilderKey)
	inst.MustBlock(t, "an expired attestation", gk.BankToken, gk.DecideRequest{Image: pinned, Attestation: &expired})

	stranger := *gk.Attest(digest)
	stranger.Builder = "someone-else"
	stranger.Signature = gk.Sign(stranger, gk.BuilderKey)
	inst.MustBlock(t, "an attestation from an unknown builder", gk.BankToken, gk.DecideRequest{Image: pinned, Attestation: &stranger})
}

func TestAuditShowsOnlyTheCallersDecisions(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	inst.Decide(t, gk.AcmeToken, gk.DecideRequest{Image: "cgr.dev/chainguard/nginx:1.27"})
	inst.Decide(t, gk.AcmeToken, gk.DecideRequest{Image: "docker.io/library/nginx:1.27"})

	status, body := inst.Do(t, http.MethodGet, "/v1/audit?limit=10", gk.AcmeToken, nil)
	var resp struct {
		Entries []struct {
			Tenant string `json:"tenant"`
			Image  string `json:"image"`
			Allow  bool   `json:"allow"`
		} `json:"entries"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &resp) != nil {
		t.Fatalf("GET /v1/audit returned %d %s", status, body)
	}
	if len(resp.Entries) != 2 || resp.Entries[0].Image != "docker.io/library/nginx:1.27" || resp.Entries[0].Allow || !resp.Entries[1].Allow {
		t.Errorf("acme's audit trail = %+v, want the denied docker.io image first, then the allowed one", resp.Entries)
	}

	status, body = inst.Do(t, http.MethodGet, "/v1/audit", gk.BankToken, nil)
	if status != http.StatusOK || strings.Contains(string(body), "nginx") {
		t.Errorf("bank's audit trail returned %d %s, want no entries", status, body)
	}
}

func TestLogsAndArchives(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	gk.WriteFile(t, filepath.Join(inst.LogDir, "build-1.log"), "build one\n")
	gk.WriteFile(t, filepath.Join(inst.LogDir, "notes.txt"), "not a log\n")
	gk.WriteArchive(t, filepath.Join(inst.ArchiveDir, "2026-02.tar.gz"), "build-7.log", "build-8.log")

	status, body := inst.Do(t, http.MethodGet, "/v1/logs", gk.AcmeToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "build-1.log") || strings.Contains(string(body), "notes.txt") {
		t.Errorf("listing logs returned %d %s, want only build-1.log", status, body)
	}
	status, body = inst.Do(t, http.MethodGet, "/v1/logs?name=build-1.log", gk.AcmeToken, nil)
	if status != http.StatusOK || string(body) != "build one\n" {
		t.Errorf("reading build-1.log returned %d %q", status, body)
	}
	status, body = inst.Do(t, http.MethodGet, "/v1/archives?name=2026-02.tar.gz", gk.BankToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "build-7.log") || !strings.Contains(string(body), "build-8.log") {
		t.Errorf("listing the archive returned %d %s", status, body)
	}
}

func TestAdminAPI(t *testing.T) {
	inst := gk.Start(t, gk.Options{})

	for _, path := range []string{"/admin/policies", "/admin/policies/acme"} {
		for what, token := range map[string]string{"no token": "", "a tenant token": gk.AcmeToken} {
			if status, _ := inst.Do(t, http.MethodGet, path, token, nil); status != http.StatusUnauthorized {
				t.Errorf("GET %s with %s returned %d, want 401", path, what, status)
			}
		}
	}
	if status, _ := inst.Do(t, http.MethodPost, "/admin/cache/flush", gk.AcmeToken, nil); status != http.StatusUnauthorized {
		t.Errorf("POST /admin/cache/flush with a tenant token returned %d, want 401", status)
	}

	image := gk.DecideRequest{Image: "cgr.dev/chainguard/nginx:1.27"}
	inst.Decide(t, gk.AcmeToken, image)
	if status, _ := inst.Do(t, http.MethodPost, "/admin/cache/flush", gk.AdminToken, nil); status != http.StatusNoContent {
		t.Errorf("POST /admin/cache/flush returned %d, want 204", status)
	}
	if status, resp := inst.Decide(t, gk.AcmeToken, image); status != http.StatusOK || resp.Cached {
		t.Errorf("a decision right after a flush: status %d, cached=%v; want an uncached answer", status, resp.Cached)
	}

	if status, _ := inst.Do(t, http.MethodPut, "/admin/policies/nobody", gk.AdminToken, gk.AcmePolicy()); status != http.StatusNotFound {
		t.Errorf("updating an unknown tenant returned %d, want 404", status)
	}
	if status, _ := inst.Do(t, http.MethodPut, "/admin/policies/acme", gk.AdminToken, gk.Policy{}); status != http.StatusBadRequest {
		t.Errorf("an empty policy returned %d, want 400", status)
	}
}
