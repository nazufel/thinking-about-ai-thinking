package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseFlags(t *testing.T) {
	o, err := parseFlags([]string{"-config", "/etc/gatekeeper.json", "-listen", ":9000", "-registry", "ghcr.io", "-debug"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := options{configPath: "/etc/gatekeeper.json", listen: ":9000", registry: "ghcr.io", debug: true}
	if o != want {
		t.Errorf("parseFlags = %+v, want %+v", o, want)
	}
}

func TestParseFlagsRejectsUnknown(t *testing.T) {
	if _, err := parseFlags([]string{"-no-such-flag"}, io.Discard); err == nil {
		t.Error("an unknown flag was accepted")
	}
	if _, err := parseFlags([]string{"extra"}, io.Discard); err == nil {
		t.Error("a stray argument was accepted")
	}
	if _, err := parseFlags([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h returned %v, want flag.ErrHelp", err)
	}
}

func TestLoadConfigAppliesOverrides(t *testing.T) {
	path := writeConfig(t, `{"listen": "127.0.0.1:8080", "registry": "cgr.dev"}`)

	cfg, err := loadConfig(options{configPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" || cfg.Registry != "cgr.dev" || cfg.Debug {
		t.Errorf("without overrides the config was %+v", cfg)
	}

	cfg, err = loadConfig(options{configPath: path, listen: ":9000", registry: "ghcr.io", debug: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9000" {
		t.Errorf("Listen = %q, want the -listen override", cfg.Listen)
	}
	if cfg.Registry != "ghcr.io" {
		t.Errorf("Registry = %q, want the -registry override", cfg.Registry)
	}
	if !cfg.Debug {
		t.Error("-debug did not turn debug mode on")
	}
}

func TestRunReportsBadConfig(t *testing.T) {
	if err := run([]string{"-config", filepath.Join(t.TempDir(), "missing.json")}, io.Discard); err == nil {
		t.Error("run started without a configuration file")
	}
	path := writeConfig(t, `{"registry": ""}`)
	if err := run([]string{"-config", path}, io.Discard); err == nil {
		t.Error("run started with an invalid configuration")
	}
}
