package ref

import (
	"errors"
	"strings"
	"testing"
)

const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Reference
	}{
		{"cgr.dev/chainguard/nginx:1.27", Reference{"cgr.dev", "chainguard/nginx", "1.27", ""}},
		{"chainguard/nginx:1.27", Reference{"cgr.dev", "chainguard/nginx", "1.27", ""}},
		{"nginx", Reference{"cgr.dev", "nginx", "latest", ""}},
		{"GHCR.io/org/app:v2", Reference{"ghcr.io", "org/app", "v2", ""}},
		{"localhost:5000/app", Reference{"localhost:5000", "app", "latest", ""}},
		{"localhost:5000/app:dev", Reference{"localhost:5000", "app", "dev", ""}},
		{"cgr.dev/chainguard/nginx@" + digest, Reference{"cgr.dev", "chainguard/nginx", "", digest}},
		{"cgr.dev/chainguard/nginx:1.27@" + digest, Reference{"cgr.dev", "chainguard/nginx", "1.27", digest}},
		{"  cgr.dev/chainguard/go:1.22  ", Reference{"cgr.dev", "chainguard/go", "1.22", ""}},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in, "cgr.dev")
		if err != nil {
			t.Errorf("Parse(%q) returned error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"cgr.dev/Chainguard/nginx",
		"cgr.dev/chainguard/nginx:",
		"cgr.dev/chainguard/nginx@sha256:abc",
		"cgr.dev/chainguard/nginx@md5:" + strings.Repeat("a", 64),
		"cgr.dev//nginx",
		"cgr.dev/chainguard/nginx:bad tag",
	} {
		if _, err := Parse(in, "cgr.dev"); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalid", in, err)
		}
	}
}

func TestStringRoundTrips(t *testing.T) {
	for _, in := range []string{
		"cgr.dev/chainguard/nginx:1.27",
		"cgr.dev/chainguard/nginx@" + digest,
		"cgr.dev/chainguard/nginx:1.27@" + digest,
		"localhost:5000/app:dev",
	} {
		r, err := Parse(in, "cgr.dev")
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got := r.String(); got != in {
			t.Errorf("String() = %q, want %q", got, in)
		}
	}
}

func TestPinned(t *testing.T) {
	tagged, _ := Parse("cgr.dev/chainguard/nginx:1.27", "cgr.dev")
	if tagged.Pinned() {
		t.Error("a tagged reference reported itself as pinned")
	}
	pinned, _ := Parse("cgr.dev/chainguard/nginx@"+digest, "cgr.dev")
	if !pinned.Pinned() {
		t.Error("a digest reference did not report itself as pinned")
	}
}
