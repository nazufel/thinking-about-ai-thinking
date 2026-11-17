package audit

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func entry(tenant, image string, allow bool) Entry {
	return Entry{Time: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC), Tenant: tenant, Image: image, Allow: allow, Reason: "test"}
}

func TestForTenantReturnsNewestFirst(t *testing.T) {
	l, err := New("", 10)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(entry("acme", "cgr.dev/a:1", true))
	l.Record(entry("bank", "cgr.dev/b:1", false))
	l.Record(entry("acme", "cgr.dev/a:2", true))

	got := l.ForTenant("acme", 10)
	if len(got) != 2 || got[0].Image != "cgr.dev/a:2" || got[1].Image != "cgr.dev/a:1" {
		t.Errorf("ForTenant(acme) = %+v, want a:2 then a:1", got)
	}
	if got := l.ForTenant("acme", 1); len(got) != 1 {
		t.Errorf("ForTenant with limit 1 returned %d entries", len(got))
	}
	if got := l.ForTenant("nobody", 10); len(got) != 0 {
		t.Errorf("ForTenant for an unknown tenant returned %d entries", len(got))
	}
}

func TestKeepsOnlyTheNewestEntries(t *testing.T) {
	l, err := New("", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{"cgr.dev/a:1", "cgr.dev/a:2", "cgr.dev/a:3"} {
		l.Record(entry("acme", image, true))
	}
	got := l.ForTenant("acme", 10)
	if len(got) != 2 || got[0].Image != "cgr.dev/a:3" {
		t.Errorf("ForTenant = %+v, want the two newest entries", got)
	}
}

func TestAppendsToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := New(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(entry("acme", "cgr.dev/a:1", true))
	l.Record(entry("acme", "cgr.dev/a:2", false))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lines := 0
	for sc := bufio.NewScanner(f); sc.Scan(); {
		lines++
	}
	if lines != 2 {
		t.Errorf("the audit file has %d lines, want 2", lines)
	}
}
