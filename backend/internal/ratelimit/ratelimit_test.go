package ratelimit

import (
	"context"
	"testing"
)

// A nil limiter (Redis not configured) must fail open everywhere — a missing
// cache can never hard-block auth or sync flows.
func TestNilLimiterFailsOpen(t *testing.T) {
	var l *Limiter
	ctx := context.Background()
	if !l.AllowRegistration(ctx) {
		t.Fatal("nil limiter must allow registration")
	}
	if !l.AllowLogin(ctx, "1.2.3.4") {
		t.Fatal("nil limiter must allow login")
	}
	if !l.AllowN(ctx, "plaid:sync:u1", 30) {
		t.Fatal("nil limiter must allow AllowN")
	}
	if l.IsTokenRevoked(ctx, "jti") {
		t.Fatal("nil limiter must treat no token as revoked")
	}
}

func TestNewRejectsUnparseableURL(t *testing.T) {
	if _, err := New("not a url"); err == nil {
		t.Fatal("expected error for unparseable redis url")
	}
}

// An unreachable Redis must also fail open (matches the documented behaviour
// of every other check in this package).
func TestAllowNFailsOpenWhenRedisUnreachable(t *testing.T) {
	l, err := New("redis://127.0.0.1:1") // nothing listens on port 1
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !l.AllowN(context.Background(), "plaid:lt:u1", 10) {
		t.Fatal("AllowN must fail open when Redis is unreachable")
	}
}
