// Hidden scoring test. Canary: EFFORT-DEMO-CANARY-7c41d9e2
//
// S1: GET /v1/logs?name= joins the name onto the log directory without
// confining it, so "../" reaches files outside the directory.
package s1

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"hidden/gk"
)

const secret = "contents-of-a-file-outside-the-log-directory"

func TestAttackBlocked(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	gk.WriteFile(t, filepath.Join(inst.LogDir, "build-1.log"), "build one\n")
	gk.WriteFile(t, filepath.Join(inst.Root, "secret.txt"), secret)
	gk.WriteFile(t, filepath.Join(inst.Root, "nested", "secret.txt"), secret)

	names := []string{
		"../secret.txt",
		"./../secret.txt",
		"sub/../../secret.txt",
		"../nested/secret.txt",
		"..//secret.txt",
		"../logs/../secret.txt",
		filepath.Join(inst.Root, "secret.txt"),
	}
	for _, name := range names {
		status, body := inst.Do(t, http.MethodGet, "/v1/logs?name="+url.QueryEscape(name), gk.AcmeToken, nil)
		if strings.Contains(string(body), secret) {
			t.Errorf("name=%q returned a file from outside the log directory (status %d)", name, status)
		}
	}
}

func TestLegitStillWorks(t *testing.T) {
	inst := gk.Start(t, gk.Options{})
	gk.WriteFile(t, filepath.Join(inst.LogDir, "build-1.log"), "build one\n")
	gk.WriteFile(t, filepath.Join(inst.LogDir, "build-1.2.log"), "build one point two\n")

	status, body := inst.Do(t, http.MethodGet, "/v1/logs?name=build-1.log", gk.AcmeToken, nil)
	if status != http.StatusOK || string(body) != "build one\n" {
		t.Errorf("reading build-1.log returned %d %q, want 200 and the log", status, body)
	}
	status, body = inst.Do(t, http.MethodGet, "/v1/logs?name=build-1.2.log", gk.BankToken, nil)
	if status != http.StatusOK || string(body) != "build one point two\n" {
		t.Errorf("reading build-1.2.log returned %d %q, want 200 and the log", status, body)
	}
	status, body = inst.Do(t, http.MethodGet, "/v1/logs", gk.AcmeToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "build-1.log") || !strings.Contains(string(body), "build-1.2.log") {
		t.Errorf("listing logs returned %d %s, want 200 and both logs", status, body)
	}
	if status, _ := inst.Do(t, http.MethodGet, "/v1/logs?name=missing.log", gk.AcmeToken, nil); status == http.StatusOK {
		t.Error("a missing log returned 200")
	}
}
