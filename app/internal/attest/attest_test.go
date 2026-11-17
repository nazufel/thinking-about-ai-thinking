package attest

import (
	"errors"
	"testing"
	"time"
)

const testDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"

var (
	testKey = []byte("builder-key-for-tests")
	testNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
)

func newTestVerifier() *Verifier {
	v := NewVerifier(map[string][]byte{"ci-main": testKey}, 24*time.Hour)
	v.SetClock(func() time.Time { return testNow })
	return v
}

func signed(builder, digest string, issued time.Time, key []byte) Attestation {
	a := Attestation{Builder: builder, Digest: digest, IssuedAt: issued.Unix()}
	a.Signature = Sign(a, key)
	return a
}

func TestVerifyAcceptsValidAttestation(t *testing.T) {
	a := signed("ci-main", testDigest, testNow.Add(-time.Hour), testKey)
	if err := newTestVerifier().Verify(a); err != nil {
		t.Fatalf("Verify returned %v for a valid attestation", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	valid := signed("ci-main", testDigest, testNow.Add(-time.Hour), testKey)

	tampered := valid
	tampered.Digest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"

	notHex := valid
	notHex.Signature = "not-hex"

	tests := []struct {
		name string
		att  Attestation
		want error
	}{
		{"unknown builder", signed("someone-else", testDigest, testNow, testKey), ErrUnknownBuilder},
		{"wrong key", signed("ci-main", testDigest, testNow, []byte("another-key")), ErrBadSignature},
		{"tampered digest", tampered, ErrBadSignature},
		{"signature is not hex", notHex, ErrBadSignature},
		{"expired", signed("ci-main", testDigest, testNow.Add(-25*time.Hour), testKey), ErrExpired},
		{"from the future", signed("ci-main", testDigest, testNow.Add(10*time.Minute), testKey), ErrNotYetValid},
	}
	for _, tt := range tests {
		if err := newTestVerifier().Verify(tt.att); !errors.Is(err, tt.want) {
			t.Errorf("%s: Verify error = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestVerifyAllowsSmallClockSkew(t *testing.T) {
	a := signed("ci-main", testDigest, testNow.Add(time.Minute), testKey)
	if err := newTestVerifier().Verify(a); err != nil {
		t.Fatalf("Verify returned %v for an attestation one minute ahead", err)
	}
}

func TestPayloadIsStable(t *testing.T) {
	a := Attestation{Builder: "ci-main", Digest: testDigest, IssuedAt: 1700000000}
	want := "ci-main\n" + testDigest + "\n1700000000"
	if got := string(Payload(a)); got != want {
		t.Errorf("Payload = %q, want %q", got, want)
	}
}
