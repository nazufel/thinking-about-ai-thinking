package ratelimit

import (
	"testing"
	"time"
)

func TestNilLimiterAllowsEverything(t *testing.T) {
	var l *Limiter
	for i := 0; i < 100; i++ {
		if !l.Allow("acme") {
			t.Fatal("a nil limiter refused a request")
		}
	}
	if New(0) != nil {
		t.Error("New(0) returned a limiter; want nil")
	}
}

func TestAllowRefillsOverTime(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	l := New(3)
	l.SetClock(func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if !l.Allow("acme") {
			t.Fatalf("request %d was refused inside the burst", i+1)
		}
	}
	if l.Allow("acme") {
		t.Fatal("a request beyond the burst was admitted")
	}
	if !l.Allow("bank") {
		t.Fatal("one caller used up another caller's allowance")
	}

	now = now.Add(20 * time.Second)
	if !l.Allow("acme") {
		t.Error("no request was admitted after a third of a minute")
	}
	if l.Allow("acme") {
		t.Error("two requests were admitted after a third of a minute")
	}
}
