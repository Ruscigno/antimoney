package ratelimit

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	registrationDailyCap = 50 // global new accounts per UTC day
	loginPerMinuteCap    = 10 // login attempts per IP per minute
)

// Limiter is a Redis-backed rate limiter. All methods fail open when Redis
// is unavailable so that a Redis outage never hard-blocks auth flows.
type Limiter struct {
	rdb *redis.Client
}

// New connects to the given Redis URL and returns a Limiter.
// Returns an error only if the URL is unparseable; connection errors surface
// lazily and cause fail-open behaviour.
func New(redisURL string) (*Limiter, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: parse redis url: %w", err)
	}
	return &Limiter{rdb: redis.NewClient(opts)}, nil
}

// AllowRegistration checks the global daily cap on new account creation (50/day).
// The counter resets at midnight UTC by using the UTC date as part of the key.
// Returns true (allow) or false (deny). Fails open on Redis errors.
func (l *Limiter) AllowRegistration(ctx context.Context) bool {
	if l == nil {
		return true
	}
	key := fmt.Sprintf("reg:daily:%s", time.Now().UTC().Format("2006-01-02"))
	count, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		log.Printf("ratelimit: redis error on registration check: %v", err)
		return true // fail open
	}
	if count == 1 {
		// Expire 25 h after first increment so the key is always gone before the
		// next-day counter would be created, even across DST or clock skew.
		if err := l.rdb.Expire(ctx, key, 25*time.Hour).Err(); err != nil {
			log.Printf("ratelimit: redis expire error: %v", err)
		}
	}
	return count <= registrationDailyCap
}

// AllowLogin checks per-IP login rate (10 attempts per minute).
// Uses a 1-minute sliding bucket keyed by IP + current UTC minute.
// Fails open on Redis errors.
func (l *Limiter) AllowLogin(ctx context.Context, ip string) bool {
	if l == nil {
		return true
	}
	// Key per IP per UTC minute — e.g. "login:ip:1.2.3.4:2026-04-15T14:32"
	key := fmt.Sprintf("login:ip:%s:%s", ip, time.Now().UTC().Format("2006-01-02T15:04"))
	count, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		log.Printf("ratelimit: redis error on login check: %v", err)
		return true // fail open
	}
	if count == 1 {
		// 2-minute TTL covers the full window plus a small buffer.
		if err := l.rdb.Expire(ctx, key, 2*time.Minute).Err(); err != nil {
			log.Printf("ratelimit: redis expire error: %v", err)
		}
	}
	return count <= loginPerMinuteCap
}
