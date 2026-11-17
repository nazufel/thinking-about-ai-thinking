// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// Rename task: the "registry" setting becomes "registry-url" in the flag, the
// configuration key and the documentation.
//
// GATEKEEPER_DIR must point at the source tree the binary was built from.
package rename

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hidden/gk"
)

// withoutRegistry removes the default-registry key under both names, so a
// test controls where the value comes from.
func withoutRegistry(cfg map[string]any) {
	delete(cfg, "registry")
	delete(cfg, "registry-url")
}

// An image with no registry shows which default the server is using. The
// acme tenant allows ghcr.io.
func usesDefaultRegistry(t *testing.T, inst *gk.Instance, want string) {
	t.Helper()
	status, resp := inst.Decide(t, gk.AcmeToken, gk.DecideRequest{Image: "org/app:v2"})
	if !gk.Allowed(status, resp) || resp.Image != want+"/org/app:v2" {
		t.Errorf("deciding org/app:v2: status %d, %+v; want an allow for %s/org/app:v2", status, resp, want)
	}
}

func TestNewFlagWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{
		Config:    withoutRegistry,
		ExtraArgs: []string{"-registry-url", "ghcr.io"},
	})
	usesDefaultRegistry(t, inst, "ghcr.io")
}

func TestNewConfigKeyWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{Config: func(cfg map[string]any) {
		withoutRegistry(cfg)
		cfg["registry-url"] = "ghcr.io"
	}})
	usesDefaultRegistry(t, inst, "ghcr.io")
}

func TestDefaultStillApplies(t *testing.T) {
	inst := gk.Start(t, gk.Options{Config: withoutRegistry})
	usesDefaultRegistry(t, inst, "cgr.dev")
}

var (
	oldFlagRE = regexp.MustCompile("(^|[^A-Za-z0-9_-])--?registry([^A-Za-z0-9_-]|$)")
	oldKeyRE  = regexp.MustCompile("(`registry`|\"registry\")")
)

func TestDocsUpdated(t *testing.T) {
	dir := os.Getenv("GATEKEEPER_DIR")
	if dir == "" {
		t.Fatal("GATEKEEPER_DIR is not set: point it at the service's source tree")
	}
	for _, name := range []string{"README.md", "config.example.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		text := string(raw)
		if !strings.Contains(text, "registry-url") {
			t.Errorf("%s does not mention registry-url", name)
		}
		if oldFlagRE.MatchString(text) {
			t.Errorf("%s still documents the old -registry flag", name)
		}
		if oldKeyRE.MatchString(text) {
			t.Errorf("%s still documents the old registry key", name)
		}
		if !strings.Contains(text, "allowed_registries") {
			t.Errorf("%s no longer mentions allowed_registries, which is a different setting", name)
		}
	}
}

// TestOldNameRejected is informational: it is reported but does not decide
// whether the rename counts as correct.
func TestOldNameRejected(t *testing.T) {
	// An otherwise valid configuration that uses the old key.
	oldKey := gk.Prepare(t, gk.Options{Config: func(cfg map[string]any) {
		withoutRegistry(cfg)
		cfg["registry"] = "ghcr.io"
	}})
	if refused, _ := gk.RefusesToStart(t, oldKey.Args()...); !refused {
		t.Error("a configuration file with the old registry key was accepted")
	}

	// A valid configuration with the old flag on the command line.
	oldFlag := gk.Prepare(t, gk.Options{Config: withoutRegistry, ExtraArgs: []string{"-registry", "ghcr.io"}})
	if refused, _ := gk.RefusesToStart(t, oldFlag.Args()...); !refused {
		t.Error("the old -registry flag was accepted")
	}
}
