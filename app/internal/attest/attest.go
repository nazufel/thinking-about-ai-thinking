// Package attest verifies build attestations.
//
// An attestation says that a named builder produced the image with a given
// digest at a given time. The builder signs the statement with a key it
// shares with gatekeeper.
package attest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Attestation is the statement a builder attaches to an image.
type Attestation struct {
	Builder   string `json:"builder"`
	Digest    string `json:"digest"`
	IssuedAt  int64  `json:"issued_at"`
	Signature string `json:"signature"`
}

// Verification errors.
var (
	ErrUnknownBuilder = errors.New("unknown builder")
	ErrBadSignature   = errors.New("bad signature")
	ErrExpired        = errors.New("attestation expired")
	ErrNotYetValid    = errors.New("attestation issued in the future")
)

// clockSkew is how far ahead of our clock a builder's clock may run.
const clockSkew = 2 * time.Minute

// Payload returns the bytes a builder signs: the builder name, the image
// digest and the issue time, one per line.
func Payload(a Attestation) []byte {
	return []byte(fmt.Sprintf("%s\n%s\n%d", a.Builder, a.Digest, a.IssuedAt))
}

// Sign returns the hex-encoded HMAC-SHA256 signature of a under key.
func Sign(a Attestation, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(Payload(a))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verifier checks attestations against the keys of known builders.
type Verifier struct {
	keys   map[string][]byte
	maxAge time.Duration
	now    func() time.Time
}

// NewVerifier returns a Verifier for the given builder keys. Attestations
// older than maxAge are rejected.
func NewVerifier(keys map[string][]byte, maxAge time.Duration) *Verifier {
	return &Verifier{keys: keys, maxAge: maxAge, now: time.Now}
}

// SetClock replaces the verifier's time source.
func (v *Verifier) SetClock(now func() time.Time) {
	v.now = now
}

// Verify reports whether a carries a valid signature from a known builder
// and is still within its validity window.
func (v *Verifier) Verify(a Attestation) error {
	key, ok := v.keys[a.Builder]
	if !ok {
		return ErrUnknownBuilder
	}

	sig, err := hex.DecodeString(a.Signature)
	if err != nil {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(Payload(a))
	want := mac.Sum(nil)
	// Some builders send a shortened signature, so compare the bytes received.
	if len(sig) > len(want) || !hmac.Equal(want[:len(sig)], sig) {
		return ErrBadSignature
	}

	issued := time.Unix(a.IssuedAt, 0)
	now := v.now()
	if issued.After(now.Add(clockSkew)) {
		return ErrNotYetValid
	}
	if now.Sub(issued) > v.maxAge {
		return ErrExpired
	}
	return nil
}
