// Package ref parses container image references.
package ref

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Reference is a parsed image reference.
type Reference struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
}

// ErrInvalid is returned for references that cannot be parsed.
var ErrInvalid = errors.New("invalid image reference")

var (
	registryRE   = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]{1,5})?$`)
	repositoryRE = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
	tagRE        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestRE     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// Parse splits an image reference into its parts. A reference that does not
// name a registry is resolved against defaultRegistry. A reference with
// neither a tag nor a digest gets the tag "latest".
func Parse(s, defaultRegistry string) (Reference, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Reference{}, fmt.Errorf("%w: empty", ErrInvalid)
	}

	var r Reference
	name := s
	if i := strings.LastIndex(name, "@"); i >= 0 {
		r.Digest = name[i+1:]
		name = name[:i]
		if !digestRE.MatchString(r.Digest) {
			return Reference{}, fmt.Errorf("%w: bad digest", ErrInvalid)
		}
	}

	// A colon after the last slash separates the tag. A colon before it
	// belongs to a registry port.
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		r.Tag = name[i+1:]
		name = name[:i]
		if !tagRE.MatchString(r.Tag) {
			return Reference{}, fmt.Errorf("%w: bad tag", ErrInvalid)
		}
	}

	first, rest, found := strings.Cut(name, "/")
	if found && looksLikeRegistry(first) {
		r.Registry = strings.ToLower(first)
		r.Repository = rest
	} else {
		r.Registry = strings.ToLower(defaultRegistry)
		r.Repository = name
	}

	if !registryRE.MatchString(r.Registry) {
		return Reference{}, fmt.Errorf("%w: bad registry", ErrInvalid)
	}
	if !repositoryRE.MatchString(r.Repository) {
		return Reference{}, fmt.Errorf("%w: bad repository", ErrInvalid)
	}
	if r.Tag == "" && r.Digest == "" {
		r.Tag = "latest"
	}
	return r, nil
}

// looksLikeRegistry reports whether the first path component of a reference
// is a registry host and not the first part of a repository name.
func looksLikeRegistry(s string) bool {
	return strings.ContainsAny(s, ".:") || s == "localhost"
}

// String returns the canonical form of the reference.
func (r Reference) String() string {
	var b strings.Builder
	b.WriteString(r.Registry)
	b.WriteByte('/')
	b.WriteString(r.Repository)
	if r.Tag != "" {
		b.WriteByte(':')
		b.WriteString(r.Tag)
	}
	if r.Digest != "" {
		b.WriteByte('@')
		b.WriteString(r.Digest)
	}
	return b.String()
}

// Pinned reports whether the reference names an immutable digest.
func (r Reference) Pinned() bool {
	return r.Digest != ""
}
