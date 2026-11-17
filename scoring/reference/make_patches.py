#!/usr/bin/env python3
"""Regenerate the reference patches from the fixture in app/.

Run this after any change to app/:

    python3 scoring/reference/make_patches.py

It writes:

    scoring/reference/bughunt.patch     every planted bug fixed
    scoring/reference/bugs/<ID>.patch   one planted bug fixed, nothing else
    scoring/reference/rename.patch      the rename task done

Each fix is a list of exact string replacements. A replacement that does not
match exactly once stops the script, so a patch can never silently go stale.
The harness's selfcheck then proves that the patches score as they should.

Canary: EFFORT-DEMO-CANARY-7c41d9e2 (this file is scoring material).
"""
import pathlib
import shutil
import subprocess
import tempfile

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent.parent
APP = ROOT / "app"


def replace(base, rel, old, new):
    path = base / rel
    text = path.read_text()
    found = text.count(old)
    if found != 1:
        raise SystemExit(f"{rel}: expected exactly one match, found {found}, for:\n{old}")
    path.write_text(text.replace(old, new))


def ensure_resolve(b):
    """Add the name-confinement helper to the logs package, once."""
    path = b / "internal/logs/logs.go"
    if "func resolve(" in path.read_text():
        return
    replace(b, "internal/logs/logs.go", '''// ErrTooLarge is returned for logs bigger than maxLogSize.
var ErrTooLarge = errors.New("log is too large")
''', '''// ErrTooLarge is returned for logs bigger than maxLogSize.
var ErrTooLarge = errors.New("log is too large")

// ErrInvalidName is returned for names that are not a plain file name.
var ErrInvalidName = errors.New("invalid name")

// resolve returns the path of the file called name directly inside dir. It
// rejects anything that is not a plain file name, so a caller cannot reach
// outside dir.
func resolve(dir, name string) (string, error) {
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) || strings.ContainsAny(name, `/\\`) {
		return "", ErrInvalidName
	}
	return filepath.Join(dir, name), nil
}
''')


def fix_s1(b):
    """S1: confine build-log names to the log directory."""
    ensure_resolve(b)
    replace(b, "internal/logs/logs.go", '''	path := filepath.Join(s.dir, name)
	info, err := os.Stat(path)
''', '''	path, err := resolve(s.dir, name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
''')
    replace(b, "internal/server/handlers.go", '''	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such log")
		return
''', '''	case errors.Is(err, logs.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid log name")
		return
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such log")
		return
''')


def fix_s2(b):
    """S2: list archives in Go instead of through a shell."""
    ensure_resolve(b)
    replace(b, "internal/logs/logs.go", '''import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)
''', '''import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)
''')
    replace(b, "internal/logs/logs.go", '''// maxLogSize is the largest build log Read will return.
const maxLogSize = 5 << 20
''', '''// maxLogSize is the largest build log Read will return.
const maxLogSize = 5 << 20

// maxArchiveEntries is the most entries ArchiveEntries will list.
const maxArchiveEntries = 10000
''')
    replace(b, "internal/logs/logs.go", '''	path := filepath.Join(s.archiveDir, name)
	out, err := exec.Command("sh", "-c", "tar -tzf "+path).Output()
	if err != nil {
		return nil, fmt.Errorf("list archive: %w", err)
	}
	entries := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\\n") {
		if line != "" {
			entries = append(entries, line)
		}
	}
	return entries, nil
''', '''	path, err := resolve(s.archiveDir, name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()

	entries := []string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		entries = append(entries, hdr.Name)
		if len(entries) > maxArchiveEntries {
			return nil, errors.New("archive has too many entries")
		}
	}
	return entries, nil
''')


def fix_s3(b):
    """S3: the debug endpoint needs the admin token and never shows secrets."""
    replace(b, "internal/server/server.go", '''		mux.HandleFunc("GET /debug/config", s.handleDebugConfig)
''', '''		mux.Handle("GET /debug/config", s.adminAuth(http.HandlerFunc(s.handleDebugConfig)))
''')
    replace(b, "internal/server/server.go", '''// handleDebugConfig returns the settings the server is running with. It is
// only registered when debug mode is on.
func (s *Server) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}
''', '''// handleDebugConfig returns the settings the server is running with, with
// every token and key blanked out. It is only registered when debug mode is
// on, and only admins may call it.
func (s *Server) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, redacted(s.cfg))
}

// redacted returns a copy of cfg with its secrets replaced.
func redacted(cfg config.Config) config.Config {
	const hidden = "[redacted]"
	out := cfg
	out.AdminToken = hidden
	out.Tenants = make([]config.Tenant, len(cfg.Tenants))
	for i, t := range cfg.Tenants {
		t.Token = hidden
		out.Tenants[i] = t
	}
	out.Builders = make([]config.Builder, len(cfg.Builders))
	for i, b := range cfg.Builders {
		b.Key = hidden
		out.Builders[i] = b
	}
	return out
}
''')


def fix_l1(b):
    """L1: match the registry host exactly, with or without a port."""
    replace(b, "internal/policy/policy.go", '''func registryAllowed(allowed []string, registry string) bool {
	for _, a := range allowed {
		if strings.HasPrefix(registry, strings.ToLower(a)) {
			return true
		}
	}
	return false
}
''', '''func registryAllowed(allowed []string, registry string) bool {
	host := registry
	if i := strings.LastIndex(registry, ":"); i >= 0 {
		host = registry[:i]
	}
	for _, a := range allowed {
		a = strings.ToLower(a)
		if registry == a || host == a {
			return true
		}
	}
	return false
}
''')


def fix_l2(b):
    """L2: the policy update route goes behind the admin token."""
    replace(b, "internal/server/server.go", '''	admin.HandleFunc("POST /admin/cache/flush", s.handleFlushCache)
	mux.Handle("/admin/", s.adminAuth(admin))

	// Policy updates pushed by the deploy pipeline.
	mux.HandleFunc("PUT /admin/policies/{tenant}", s.handlePutPolicy)
''', '''	admin.HandleFunc("PUT /admin/policies/{tenant}", s.handlePutPolicy)
	admin.HandleFunc("POST /admin/cache/flush", s.handleFlushCache)
	mux.Handle("/admin/", s.adminAuth(admin))
''')


def fix_l3(b):
    """L3: the whole signature must match."""
    replace(b, "internal/attest/attest.go", '''	// Some builders send a shortened signature, so compare the bytes received.
	if len(sig) > len(want) || !hmac.Equal(want[:len(sig)], sig) {
		return ErrBadSignature
	}
''', '''	if !hmac.Equal(want, sig) {
		return ErrBadSignature
	}
''')


def fix_x1(b):
    """X1: an attestation only counts for the image it names."""
    replace(b, "internal/policy/policy.go", '''		case !slices.Contains(p.TrustedBuilders, att.Builder):
			return deny("builder %s is not trusted", att.Builder)
		}
''', '''		case !slices.Contains(p.TrustedBuilders, att.Builder):
			return deny("builder %s is not trusted", att.Builder)
		case !r.Pinned() || att.Digest != r.Digest:
			return deny("attestation is not for this image digest")
		}
''')


def fix_x2(b):
    """X2: decisions are cached per tenant."""
    replace(b, "internal/cache/cache.go", '''// Key builds the cache key for a decision request. Requests that carry
// different attestations are kept apart.
func Key(r ref.Reference, att *attest.Attestation) string {
	key := r.String()
''', '''// Key builds the cache key for a decision request. Decisions depend on the
// tenant's policy, so each tenant has its own entries. Requests that carry
// different attestations are kept apart too.
func Key(tenant string, r ref.Reference, att *attest.Attestation) string {
	key := tenant + "|" + r.String()
''')
    replace(b, "internal/cache/cache_test.go", '''	if Key(a, nil) == Key(b, nil) {
		t.Error("two different images share a key")
	}
	if Key(a, nil) == Key(a, att1) {
		t.Error("a request with an attestation shares a key with one without")
	}
	if Key(a, att1) == Key(a, att2) {
		t.Error("two different attestations share a key")
	}
	if Key(a, att1) != Key(a, att1) {
		t.Error("the same request produced two different keys")
	}
''', '''	if Key("acme", a, nil) == Key("acme", b, nil) {
		t.Error("two different images share a key")
	}
	if Key("acme", a, nil) == Key("bank", a, nil) {
		t.Error("two tenants share a key for the same image")
	}
	if Key("acme", a, nil) == Key("acme", a, att1) {
		t.Error("a request with an attestation shares a key with one without")
	}
	if Key("acme", a, att1) == Key("acme", a, att2) {
		t.Error("two different attestations share a key")
	}
	if Key("acme", a, att1) != Key("acme", a, att1) {
		t.Error("the same request produced two different keys")
	}
''')
    replace(b, "internal/server/decide.go", '''	key := cache.Key(reference, req.Attestation)
''', '''	key := cache.Key(tenant, reference, req.Attestation)
''')


def fix_x3(b):
    """X3: the tenant comes from the token, never from the request body."""
    replace(b, "internal/server/decide.go", '''	tenant := req.Tenant
	if tenant == "" {
		tenant = tenantFrom(r.Context())
	}
''', '''	tenant := tenantFrom(r.Context())
	if req.Tenant != "" && req.Tenant != tenant {
		writeError(w, http.StatusForbidden, "tenant does not match the token")
		return
	}
''')
    replace(b, "internal/server/decide.go", '''	// Tenant is optional. Older clients send it; newer ones rely on their
	// token alone.
''', '''	// Tenant is optional. Older clients send it; when present it must name
	// the tenant the token belongs to.
''')


BUG_FIXES = {
    "S1": fix_s1, "S2": fix_s2, "S3": fix_s3,
    "L1": fix_l1, "L2": fix_l2, "L3": fix_l3,
    "X1": fix_x1, "X2": fix_x2, "X3": fix_x3,
}


def fix_rename(b):
    """The rename task: the registry setting becomes registry-url."""
    for rel, old, new in [
        ("internal/config/config.go", '''	// Registry is the registry assumed for image references that do not
	// name one.
	Registry string `json:"registry"`
''', '''	// RegistryURL is the registry assumed for image references that do not
	// name one.
	RegistryURL string `json:"registry-url"`
'''),
        ("internal/config/config.go", '''		Registry:               "cgr.dev",
''', '''		RegistryURL:            "cgr.dev",
'''),
        ("internal/config/config.go", '''	if c.Registry == "" {
		return errors.New("registry must be set")
	}
''', '''	if c.RegistryURL == "" {
		return errors.New("registry-url must be set")
	}
'''),
        ("internal/server/decide.go", "ref.Parse(req.Image, s.cfg.Registry)", "ref.Parse(req.Image, s.cfg.RegistryURL)"),
        ("cmd/gatekeeper/main.go", '''	registry   string
	debug      bool
''', '''	registryURL string
	debug       bool
'''),
        ("cmd/gatekeeper/main.go", '''	fs.StringVar(&o.registry, "registry", "", "registry assumed''', '''	fs.StringVar(&o.registryURL, "registry-url", "", "registry assumed'''),
        ("cmd/gatekeeper/main.go", '''	if o.registry != "" {
		cfg.Registry = o.registry
	}
''', '''	if o.registryURL != "" {
		cfg.RegistryURL = o.registryURL
	}
'''),
        ("cmd/gatekeeper/main_test.go", '''"-registry", "ghcr.io", "-debug"''', '''"-registry-url", "ghcr.io", "-debug"'''),
        ("cmd/gatekeeper/main_test.go", '''listen: ":9000", registry: "ghcr.io", debug: true}
	if o != want''', '''listen: ":9000", registryURL: "ghcr.io", debug: true}
	if o != want'''),
        ("cmd/gatekeeper/main_test.go", '''`{"listen": "127.0.0.1:8080", "registry": "cgr.dev"}`''', '''`{"listen": "127.0.0.1:8080", "registry-url": "cgr.dev"}`'''),
        ("cmd/gatekeeper/main_test.go", '''cfg.Registry != "cgr.dev" || cfg.Debug''', '''cfg.RegistryURL != "cgr.dev" || cfg.Debug'''),
        ("cmd/gatekeeper/main_test.go", '''listen: ":9000", registry: "ghcr.io", debug: true})
	if err != nil''', '''listen: ":9000", registryURL: "ghcr.io", debug: true})
	if err != nil'''),
        ("cmd/gatekeeper/main_test.go", '''	if cfg.Registry != "ghcr.io" {
		t.Errorf("Registry = %q, want the -registry override", cfg.Registry)
	}
''', '''	if cfg.RegistryURL != "ghcr.io" {
		t.Errorf("RegistryURL = %q, want the -registry-url override", cfg.RegistryURL)
	}
'''),
        ("cmd/gatekeeper/main_test.go", '''`{"registry": ""}`''', '''`{"registry-url": ""}`'''),
        ("internal/config/config_test.go", '''	if cfg.Registry != "cgr.dev" {
		t.Errorf("Registry = %q, want the default cgr.dev", cfg.Registry)
	}
''', '''	if cfg.RegistryURL != "cgr.dev" {
		t.Errorf("RegistryURL = %q, want the default cgr.dev", cfg.RegistryURL)
	}
'''),
        ("internal/config/config_test.go", '''func TestLoadReadsRegistry(t *testing.T) {''', '''func TestLoadReadsRegistryURL(t *testing.T) {'''),
        ("internal/config/config_test.go", '''`{"registry": "ghcr.io"}`''', '''`{"registry-url": "ghcr.io"}`'''),
        ("internal/config/config_test.go", '''	if cfg.Registry != "ghcr.io" {
		t.Errorf("Registry = %q, want ghcr.io", cfg.Registry)
	}
''', '''	if cfg.RegistryURL != "ghcr.io" {
		t.Errorf("RegistryURL = %q, want ghcr.io", cfg.RegistryURL)
	}
'''),
        ("internal/config/config_test.go", '''{"no registry", func(c *Config) { c.Registry = "" }, "registry"},''', '''{"no registry", func(c *Config) { c.RegistryURL = "" }, "registry-url"},'''),
        ("config.example.json", '''  "registry": "cgr.dev",''', '''  "registry-url": "cgr.dev",'''),
        ("README.md", '''| `-registry` | Registry assumed''', '''| `-registry-url` | Registry assumed'''),
        ("README.md", '''| `registry` | Registry assumed''', '''| `registry-url` | Registry assumed'''),
        ("README.md", '''For example, with `"registry": "cgr.dev"` the reference''', '''For example, with `"registry-url": "cgr.dev"` the reference'''),
    ]:
        replace(b, rel, old, new)


def build(dest, fixes):
    """Write the patch that turns app/ into app/ with the given fixes."""
    with tempfile.TemporaryDirectory() as tmp:
        work = pathlib.Path(tmp)
        shutil.copytree(APP, work / "a")
        shutil.copytree(APP, work / "b")
        for fix in fixes:
            fix(work / "b")
        subprocess.run(["gofmt", "-w", "."], cwd=work / "b", check=True)
        out = subprocess.run(
            ["git", "diff", "--no-index", "--no-color", "a", "b"],
            cwd=work, capture_output=True, text=True,
        )
        if out.returncode not in (0, 1):
            raise SystemExit(out.stderr)
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(out.stdout)
    print(f"wrote {dest.relative_to(ROOT)} ({len(out.stdout.splitlines())} lines)")


if __name__ == "__main__":
    build(HERE / "bughunt.patch", list(BUG_FIXES.values()))
    for bug_id, fix in BUG_FIXES.items():
        build(HERE / "bugs" / f"{bug_id}.patch", [fix])
    build(HERE / "rename.patch", [fix_rename])
