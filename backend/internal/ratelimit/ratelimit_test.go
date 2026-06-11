package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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

// TestAllowNWindowAccounting exercises the REAL counting behaviour against an
// in-process Redis (miniredis): the budget is enforced, keys are isolated per
// caller, and the bucket expires.
func TestAllowNWindowAccounting(t *testing.T) {
	srv := miniredis.RunT(t)
	l, err := New("redis://" + srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Exactly perMinute calls pass; the next one in the same window is denied.
	for i := 0; i < 5; i++ {
		if !l.AllowN(ctx, "plaid:sync:user-a", 5) {
			t.Fatalf("call %d within budget must be allowed", i+1)
		}
	}
	if l.AllowN(ctx, "plaid:sync:user-a", 5) {
		t.Fatal("call over budget must be denied")
	}

	// Keys are isolated: another user (and another op) is unaffected.
	if !l.AllowN(ctx, "plaid:sync:user-b", 5) {
		t.Fatal("a different user must have an independent budget")
	}
	if !l.AllowN(ctx, "plaid:lt:user-a", 5) {
		t.Fatal("a different operation must have an independent budget")
	}

	// The bucket has a TTL — after the window passes, the budget resets.
	srv.FastForward(3 * time.Minute)
	if !l.AllowN(ctx, "plaid:sync:user-a", 5) {
		t.Fatal("budget must reset after the window expires")
	}
}
