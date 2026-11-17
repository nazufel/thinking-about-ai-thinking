// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// S2: GET /v1/archives?name= builds a shell command from the name, so shell
// syntax in the name runs as a command.
package s2

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hidden/gk"
)

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	gk.WriteArchive(t, filepath.Join(inst.ArchiveDir, "2026-02.tar.gz"), "build-7.log")

	// Each payload ends in .tar.gz, which the handler insists on, and tries
	// to create a marker file through the shell.
	payloads := []string{
		"x; touch %s; echo .tar.gz",
		"x$(touch %s).tar.gz",
		"x`touch %s`.tar.gz",
		"x' ; touch %s ; echo '.tar.gz",
		"x\" ; touch %s ; echo \".tar.gz",
		"x\ntouch %s\necho .tar.gz",
		"x | touch %s #.tar.gz",
		"x || touch %s || echo .tar.gz",
		"2026-02.tar.gz; touch %s; echo .tar.gz",
	}
	for n, payload := range payloads {
		marker := filepath.Join(inst.Root, fmt.Sprintf("marker-%d", n))
		name := fmt.Sprintf(payload, marker)
		inst.Do(t, http.MethodGet, "/v1/archives?name="+url.QueryEscape(name), gk.AcmeToken, nil)
		// Give a command started in the background a moment to land.
		time.Sleep(100 * time.Millisecond)
		if gk.Exists(marker) {
			t.Errorf("payload %q ran a command on the server", payload)
		}
	}
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	gk.WriteArchive(t, filepath.Join(inst.ArchiveDir, "2026-02.tar.gz"), "build-7.log", "build-8.log")

	status, body := inst.Do(t, http.MethodGet, "/v1/archives?name=2026-02.tar.gz", gk.AcmeToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "build-7.log") || !strings.Contains(string(body), "build-8.log") {
		t.Errorf("listing 2026-02.tar.gz returned %d %s, want 200 and both entries", status, body)
	}
	if status, _ := inst.Do(t, http.MethodGet, "/v1/archives?name=missing.tar.gz", gk.AcmeToken, nil); status == http.StatusOK {
		t.Error("a missing archive returned 200")
	}
}
