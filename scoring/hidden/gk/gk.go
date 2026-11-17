// Package gk drives a built gatekeeper binary over HTTP for the hidden
// scoring tests.
//
// It imports nothing from the service. The tests only rely on the service's
// documented contract: its command-line flags, its configuration file and its
// HTTP API. They keep working however the internals are rearranged.
//
// Canary: EFFORT-DEMO-CANARY-7c41d9e2. This string exists only in the scoring
// material. The harness searches every run's transcript for it; a hit means
// the model saw the hidden tests and the run is void.
package gk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Canary marks the scoring material. See the package comment.
const Canary = "EFFORT-DEMO-CANARY-7c41d9e2"

// Credentials used by every test instance. They are fixtures, not secrets.
const (
	AdminToken = "admin-token-for-scoring"
	AcmeToken  = "acme-token-for-scoring"
	BankToken  = "bank-token-for-scoring"
	Builder    = "ci-main"
	BuilderKey = "ci-main-key-for-scoring"
)

// Policy mirrors the service's documented policy object.
type Policy struct {
	AllowedRegistries  []string `json:"allowed_registries"`
	BlockedTags        []string `json:"blocked_tags,omitempty"`
	RequireDigest      bool     `json:"require_digest,omitempty"`
	RequireAttestation bool     `json:"require_attestation,omitempty"`
	TrustedBuilders    []string `json:"trusted_builders,omitempty"`
}

// AcmePolicy is the permissive tenant: it only limits registries.
func AcmePolicy() Policy {
	return Policy{AllowedRegistries: []string{"cgr.dev", "ghcr.io"}}
}

// BankPolicy is the strict tenant: pinned, attested images from one registry.
func BankPolicy() Policy {
	return Policy{
		AllowedRegistries:  []string{"cgr.dev"},
		BlockedTags:        []string{"latest"},
		RequireDigest:      true,
		RequireAttestation: true,
		TrustedBuilders:    []string{Builder},
	}
}

// Attestation mirrors the service's documented attestation object.
type Attestation struct {
	Builder   string `json:"builder"`
	Digest    string `json:"digest"`
	IssuedAt  int64  `json:"issued_at"`
	Signature string `json:"signature"`
}

// Attest returns an attestation for digest, issued a minute ago and signed
// with the test builder's key as the service's README describes.
func Attest(digest string) *Attestation {
	a := &Attestation{Builder: Builder, Digest: digest, IssuedAt: time.Now().Add(-time.Minute).Unix()}
	a.Signature = Sign(*a, BuilderKey)
	return a
}

// Sign returns the documented signature of a under key: hex HMAC-SHA256 over
// builder, digest and issue time joined by newlines.
func Sign(a Attestation, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "%s\n%s\n%d", a.Builder, a.Digest, a.IssuedAt)
	return hex.EncodeToString(mac.Sum(nil))
}

// Digest returns a well-formed image digest made of one repeated hex digit.
func Digest(hexDigit byte) string {
	return "sha256:" + strings.Repeat(string(hexDigit), 64)
}

// DecideRequest is the body of POST /v1/decide.
type DecideRequest struct {
	Tenant      string       `json:"tenant,omitempty"`
	Image       string       `json:"image"`
	Attestation *Attestation `json:"attestation,omitempty"`
}

// DecideResponse is the answer of POST /v1/decide.
type DecideResponse struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"`
	Image  string `json:"image"`
	Cached bool   `json:"cached"`
}

// Options adjust the instance Start launches.
type Options struct {
	// Debug turns the service's debug mode on.
	Debug bool
	// ExtraArgs are appended to the command line.
	ExtraArgs []string
	// Config, when set, may change the configuration before it is written.
	Config func(cfg map[string]any)
}

// Instance is one gatekeeper process with its own data directories.
type Instance struct {
	URL        string
	Addr       string
	Root       string
	LogDir     string
	ArchiveDir string
	ConfigPath string

	extraArgs []string
	output    *lockedBuffer
}

// Args returns the command line that runs this instance.
func (i *Instance) Args() []string {
	return append([]string{"-config", i.ConfigPath, "-listen", i.Addr}, i.extraArgs...)
}

// RegistryKey is the configuration key that names the default registry. The
// rename task changes it, so the harness can override it.
func RegistryKey() string {
	if k := os.Getenv("GK_REGISTRY_KEY"); k != "" {
		return k
	}
	return "registry"
}

// binary returns the path of the gatekeeper binary under test.
func binary(t testing.TB) string {
	t.Helper()
	bin := os.Getenv("GATEKEEPER_BIN")
	if bin == "" {
		t.Fatal("GATEKEEPER_BIN is not set: point it at a built gatekeeper binary")
	}
	return bin
}

// Prepare creates the data directories and the configuration file for an
// instance without launching it.
func Prepare(t testing.TB, opts Options) *Instance {
	t.Helper()
	root := t.TempDir()
	inst := &Instance{
		Root:       root,
		LogDir:     filepath.Join(root, "logs"),
		ArchiveDir: filepath.Join(root, "archives"),
		ConfigPath: filepath.Join(root, "config.json"),
		extraArgs:  opts.ExtraArgs,
		output:     &lockedBuffer{},
	}
	for _, d := range []string{inst.LogDir, inst.ArchiveDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	inst.Addr = freeAddr(t)
	inst.URL = "http://" + inst.Addr

	cfg := map[string]any{
		"listen":                    inst.Addr,
		RegistryKey():               "cgr.dev",
		"log_dir":                   inst.LogDir,
		"archive_dir":               inst.ArchiveDir,
		"admin_token":               AdminToken,
		"cache_ttl_seconds":         60,
		"attestation_max_age_hours": 24,
		"rate_limit_per_minute":     0,
		"debug":                     opts.Debug,
		"builders":                  []map[string]string{{"name": Builder, "key": BuilderKey}},
		"tenants": []map[string]any{
			{"name": "acme", "token": AcmeToken, "policy": AcmePolicy()},
			{"name": "bank", "token": BankToken, "policy": BankPolicy()},
		},
	}
	if opts.Config != nil {
		opts.Config(cfg)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inst.ConfigPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return inst
}

// Start prepares an instance, launches the binary named by GATEKEEPER_BIN
// and waits until it answers. The process is stopped when the test ends.
func Start(t testing.TB, opts Options) *Instance {
	t.Helper()
	bin := binary(t)
	inst := Prepare(t, opts)

	cmd := exec.Command(bin, inst.Args()...)
	cmd.Dir = inst.Root
	cmd.Stdout = inst.output
	cmd.Stderr = inst.output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gatekeeper: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-exited
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-exited:
			t.Fatalf("gatekeeper exited during startup:\n%s", inst.output.String())
		default:
		}
		resp, err := http.Get(inst.URL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return inst
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("gatekeeper did not answer /healthz within 10s:\n%s", inst.output.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// RefusesToStart launches the binary with the given arguments and reports
// whether it exited with an error within three seconds. A process that is
// still running after that is taken to have started, and is stopped.
func RefusesToStart(t testing.TB, args ...string) (refused bool, output string) {
	t.Helper()
	bin := binary(t)
	var out lockedBuffer
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gatekeeper: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err != nil, out.String()
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
		return false, out.String()
	}
}

// freeAddr returns a loopback address with a port that was free a moment ago.
func freeAddr(t testing.TB) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// Do sends a request and returns the status code and the body. token may be
// empty. body may be nil, a string or []byte sent as is, or any value sent
// as JSON.
func (i *Instance) Do(t testing.TB, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	case []byte:
		reader = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, i.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		// Report redirects instead of following them.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v\nservice output:\n%s", method, path, err, i.output.String())
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

// Decide calls POST /v1/decide. The response is only meaningful when the
// status is 200.
func (i *Instance) Decide(t testing.TB, token string, req DecideRequest) (int, DecideResponse) {
	t.Helper()
	status, body := i.Do(t, http.MethodPost, "/v1/decide", token, req)
	var resp DecideResponse
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("POST /v1/decide returned a body that is not the documented JSON: %s", body)
		}
	}
	return status, resp
}

// Allowed reports whether a decide call ended in an allow.
func Allowed(status int, resp DecideResponse) bool {
	return status == http.StatusOK && resp.Allow
}

// MustAllow fails the test unless the request is allowed.
func (i *Instance) MustAllow(t testing.TB, what, token string, req DecideRequest) {
	t.Helper()
	status, resp := i.Decide(t, token, req)
	if !Allowed(status, resp) {
		t.Errorf("%s: want an allow, got status %d, allow=%v, reason=%q", what, status, resp.Allow, resp.Reason)
	}
}

// MustBlock fails the test if the request is allowed. A denial and an error
// status both count as blocked.
func (i *Instance) MustBlock(t testing.TB, what, token string, req DecideRequest) {
	t.Helper()
	status, resp := i.Decide(t, token, req)
	if Allowed(status, resp) {
		t.Errorf("%s: the request was allowed (reason=%q)", what, resp.Reason)
	}
}

// WriteFile writes content to path, creating parent directories.
func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// WriteArchive creates a .tar.gz at path holding one empty file per name.
func WriteArchive(t testing.TB, path string, names ...string) {
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

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// lockedBuffer collects the service's output from two streams.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
