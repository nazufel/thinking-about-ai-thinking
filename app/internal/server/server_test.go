package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gatekeeper/internal/attest"
	"gatekeeper/internal/config"
	"gatekeeper/internal/policy"
)

const (
	acmeToken  = "acme-token"
	bankToken  = "bank-token"
	adminToken = "admin-token"
	builderKey = "builder-key-for-tests"
	testDigest = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
)

// newTestServer starts a server with two tenants: acme, which only limits
// registries, and bank, which also demands pinned and attested images.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	archiveDir := filepath.Join(root, "archives")
	for _, d := range []string{logDir, archiveDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(logDir, "build-1.log"), []byte("step 1 ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeArchive(t, filepath.Join(archiveDir, "2026-02.tar.gz"), "build-7.log", "build-8.log")

	cfg := config.Default()
	cfg.LogDir = logDir
	cfg.ArchiveDir = archiveDir
	cfg.AdminToken = adminToken
	cfg.Builders = []config.Builder{{Name: "ci-main", Key: builderKey}}
	cfg.Tenants = []config.Tenant{
		{Name: "acme", Token: acmeToken, Policy: policy.Policy{
			AllowedRegistries: []string{"cgr.dev", "ghcr.io"},
		}},
		{Name: "bank", Token: bankToken, Policy: policy.Policy{
			AllowedRegistries:  []string{"cgr.dev"},
			BlockedTags:        []string{"latest"},
			RequireDigest:      true,
			RequireAttestation: true,
			TrustedBuilders:    []string{"ci-main"},
		}},
	}

	handler, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func writeArchive(t *testing.T, path string, names ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// do sends a request and returns the status code and body.
func do(t *testing.T, ts *httptest.Server, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func decide(t *testing.T, ts *httptest.Server, token string, req decideRequest) decideResponse {
	t.Helper()
	status, body := do(t, ts, http.MethodPost, "/v1/decide", token, req)
	if status != http.StatusOK {
		t.Fatalf("POST /v1/decide returned %d: %s", status, body)
	}
	var resp decideResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	if status, _ := do(t, ts, http.MethodGet, "/healthz", "", nil); status != http.StatusOK {
		t.Errorf("GET /healthz returned %d", status)
	}
}

func TestDecideAppliesRegistryPolicy(t *testing.T) {
	ts := newTestServer(t)

	allowed := decide(t, ts, acmeToken, decideRequest{Image: "chainguard/nginx:1.27"})
	if !allowed.Allow || allowed.Image != "cgr.dev/chainguard/nginx:1.27" {
		t.Errorf("decision = %+v, want an allow for cgr.dev/chainguard/nginx:1.27", allowed)
	}

	denied := decide(t, ts, acmeToken, decideRequest{Image: "docker.io/library/nginx:1.27"})
	if denied.Allow {
		t.Errorf("an image from a registry outside the allowlist was allowed: %+v", denied)
	}
}

func TestDecideRequiresTenantToken(t *testing.T) {
	ts := newTestServer(t)
	req := decideRequest{Image: "cgr.dev/chainguard/nginx:1.27"}
	for _, token := range []string{"", "wrong-token"} {
		if status, _ := do(t, ts, http.MethodPost, "/v1/decide", token, req); status != http.StatusUnauthorized {
			t.Errorf("POST /v1/decide with token %q returned %d, want 401", token, status)
		}
	}
}

func TestDecideRejectsBadInput(t *testing.T) {
	ts := newTestServer(t)
	if status, _ := do(t, ts, http.MethodPost, "/v1/decide", acmeToken, decideRequest{Image: "not a reference"}); status != http.StatusBadRequest {
		t.Errorf("an unparseable image returned %d, want 400", status)
	}
}

func TestDecideWithAttestation(t *testing.T) {
	ts := newTestServer(t)
	image := "cgr.dev/chainguard/nginx@" + testDigest

	att := attest.Attestation{Builder: "ci-main", Digest: testDigest, IssuedAt: time.Now().Add(-time.Minute).Unix()}
	att.Signature = attest.Sign(att, []byte(builderKey))

	if got := decide(t, ts, bankToken, decideRequest{Image: image, Attestation: &att}); !got.Allow {
		t.Errorf("an attested, pinned image was denied: %s", got.Reason)
	}
	if got := decide(t, ts, bankToken, decideRequest{Image: image}); got.Allow {
		t.Error("an image without an attestation was allowed for a tenant that requires one")
	}

	forged := att
	forged.Signature = attest.Sign(att, []byte("not-the-builder-key"))
	if got := decide(t, ts, bankToken, decideRequest{Image: image, Attestation: &forged}); got.Allow {
		t.Error("an attestation signed with the wrong key was accepted")
	}

	if got := decide(t, ts, bankToken, decideRequest{Image: "cgr.dev/chainguard/nginx:1.27", Attestation: &att}); got.Allow {
		t.Error("an unpinned image was allowed for a tenant that requires digests")
	}
}

func TestDecideUsesCache(t *testing.T) {
	ts := newTestServer(t)
	req := decideRequest{Image: "cgr.dev/chainguard/nginx:1.27"}
	if first := decide(t, ts, acmeToken, req); first.Cached {
		t.Error("the first decision claimed to come from the cache")
	}
	if second := decide(t, ts, acmeToken, req); !second.Cached || !second.Allow {
		t.Errorf("the repeated decision = %+v, want a cached allow", second)
	}
}

func TestAuditListsOwnDecisions(t *testing.T) {
	ts := newTestServer(t)
	decide(t, ts, acmeToken, decideRequest{Image: "cgr.dev/chainguard/nginx:1.27"})
	decide(t, ts, acmeToken, decideRequest{Image: "docker.io/library/nginx:1.27"})

	status, body := do(t, ts, http.MethodGet, "/v1/audit?limit=10", acmeToken, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/audit returned %d: %s", status, body)
	}
	var resp struct {
		Entries []struct {
			Tenant string `json:"tenant"`
			Image  string `json:"image"`
			Allow  bool   `json:"allow"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("audit returned %d entries, want 2", len(resp.Entries))
	}
	if resp.Entries[0].Image != "docker.io/library/nginx:1.27" || resp.Entries[0].Allow {
		t.Errorf("newest entry = %+v, want the denied docker.io image", resp.Entries[0])
	}

	if status, body := do(t, ts, http.MethodGet, "/v1/audit", bankToken, nil); status != http.StatusOK || !strings.Contains(string(body), `"entries":[]`) {
		t.Errorf("another tenant's audit = %d %s, want an empty list", status, body)
	}
}

func TestLogs(t *testing.T) {
	ts := newTestServer(t)

	status, body := do(t, ts, http.MethodGet, "/v1/logs", acmeToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "build-1.log") {
		t.Errorf("GET /v1/logs = %d %s, want a list with build-1.log", status, body)
	}

	status, body = do(t, ts, http.MethodGet, "/v1/logs?name=build-1.log", acmeToken, nil)
	if status != http.StatusOK || string(body) != "step 1 ok\n" {
		t.Errorf("GET /v1/logs?name=build-1.log = %d %q, want the log contents", status, body)
	}

	if status, _ := do(t, ts, http.MethodGet, "/v1/logs?name=missing.log", acmeToken, nil); status != http.StatusNotFound {
		t.Errorf("a missing log returned %d, want 404", status)
	}
	if status, _ := do(t, ts, http.MethodGet, "/v1/logs", "", nil); status != http.StatusUnauthorized {
		t.Errorf("GET /v1/logs without a token returned %d, want 401", status)
	}
}

func TestArchives(t *testing.T) {
	ts := newTestServer(t)

	status, body := do(t, ts, http.MethodGet, "/v1/archives?name=2026-02.tar.gz", acmeToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "build-7.log") || !strings.Contains(string(body), "build-8.log") {
		t.Errorf("GET /v1/archives = %d %s, want both archived logs", status, body)
	}
	if status, _ := do(t, ts, http.MethodGet, "/v1/archives?name=notes.txt", acmeToken, nil); status != http.StatusBadRequest {
		t.Errorf("a name that is not a .tar.gz returned %d, want 400", status)
	}
	if status, _ := do(t, ts, http.MethodGet, "/v1/archives", acmeToken, nil); status != http.StatusBadRequest {
		t.Errorf("a request without a name returned %d, want 400", status)
	}
}

func TestAdminRequiresAdminToken(t *testing.T) {
	ts := newTestServer(t)
	for _, token := range []string{"", acmeToken} {
		if status, _ := do(t, ts, http.MethodGet, "/admin/policies", token, nil); status != http.StatusUnauthorized {
			t.Errorf("GET /admin/policies with token %q returned %d, want 401", token, status)
		}
	}
	status, body := do(t, ts, http.MethodGet, "/admin/policies", adminToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"acme"`) || !strings.Contains(string(body), `"bank"`) {
		t.Errorf("GET /admin/policies = %d %s, want both tenants", status, body)
	}
}

func TestAdminUpdatesPolicy(t *testing.T) {
	ts := newTestServer(t)
	req := decideRequest{Image: "quay.io/org/app:v2"}
	if got := decide(t, ts, acmeToken, req); got.Allow {
		t.Fatal("quay.io was allowed before the policy change")
	}

	update := policy.Policy{AllowedRegistries: []string{"cgr.dev", "ghcr.io", "quay.io"}}
	if status, body := do(t, ts, http.MethodPut, "/admin/policies/acme", adminToken, update); status != http.StatusNoContent {
		t.Fatalf("PUT /admin/policies/acme returned %d: %s", status, body)
	}
	if got := decide(t, ts, acmeToken, req); !got.Allow {
		t.Errorf("quay.io was still denied after the policy change: %s", got.Reason)
	}

	if status, _ := do(t, ts, http.MethodPut, "/admin/policies/nobody", adminToken, update); status != http.StatusNotFound {
		t.Errorf("updating an unknown tenant returned %d, want 404", status)
	}
	if status, _ := do(t, ts, http.MethodPut, "/admin/policies/acme", adminToken, policy.Policy{}); status != http.StatusBadRequest {
		t.Errorf("an empty policy returned %d, want 400", status)
	}
}

func TestAdminFlushesCache(t *testing.T) {
	ts := newTestServer(t)
	req := decideRequest{Image: "cgr.dev/chainguard/nginx:1.27"}
	decide(t, ts, acmeToken, req)
	if status, _ := do(t, ts, http.MethodPost, "/admin/cache/flush", adminToken, nil); status != http.StatusNoContent {
		t.Fatalf("POST /admin/cache/flush returned %d", status)
	}
	if got := decide(t, ts, acmeToken, req); got.Cached {
		t.Error("a decision was served from the cache right after a flush")
	}
}
