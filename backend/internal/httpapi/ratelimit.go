package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/krunpy0/company-search/backend/internal/quota"
)

const (
	defaultMaxEntries   = 16384
	minIdleTTL          = 10 * time.Minute
	sweepEveryOps       = 256
	overflowBucketKey   = "__overflow__"
	rateLimitedCode     = "rate_limited"
	rateLimitedMessage  = "Too many requests"
)

// RateRule defines a token-bucket rate limit in requests per minute and burst capacity.
type RateRule struct {
	RPM   int
	Burst int
}

// RateLimitConfig configures tiered HTTP rate limiting for the API server.
type RateLimitConfig struct {
	Enabled    bool
	MaxEntries int
	IP         RateRule
	Auth       RateRule
	API        RateRule
	Chat       RateRule
}

// DefaultRateLimitConfig returns conservative defaults suitable for the MVP API.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:    true,
		MaxEntries: defaultMaxEntries,
		IP:         RateRule{RPM: 240, Burst: 120},
		Auth:       RateRule{RPM: 10, Burst: 15},
		API:        RateRule{RPM: 120, Burst: 60},
		Chat:       RateRule{RPM: 20, Burst: 10},
	}
}

// RateLimitConfigFromEnv reads and validates rate limiter configuration from environment variables.
func RateLimitConfigFromEnv(getenv func(string) string) (RateLimitConfig, error) {
	cfg := DefaultRateLimitConfig()
	if raw := strings.TrimSpace(getenv("RATE_LIMIT_ENABLED")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return cfg, errors.New("RATE_LIMIT_ENABLED must be a boolean")
		}
		cfg.Enabled = enabled
	}
	var err error
	if cfg.MaxEntries, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_MAX_ENTRIES", cfg.MaxEntries); err != nil {
		return cfg, err
	}
	if cfg.IP.RPM, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_IP_RPM", cfg.IP.RPM); err != nil {
		return cfg, err
	}
	if cfg.IP.Burst, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_IP_BURST", cfg.IP.Burst); err != nil {
		return cfg, err
	}
	if cfg.Auth.RPM, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_AUTH_RPM", cfg.Auth.RPM); err != nil {
		return cfg, err
	}
	if cfg.Auth.Burst, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_AUTH_BURST", cfg.Auth.Burst); err != nil {
		return cfg, err
	}
	if cfg.API.RPM, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_API_RPM", cfg.API.RPM); err != nil {
		return cfg, err
	}
	if cfg.API.Burst, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_API_BURST", cfg.API.Burst); err != nil {
		return cfg, err
	}
	if cfg.Chat.RPM, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_CHAT_RPM", cfg.Chat.RPM); err != nil {
		return cfg, err
	}
	if cfg.Chat.Burst, err = parsePositiveEnvInt(getenv, "RATE_LIMIT_CHAT_BURST", cfg.Chat.Burst); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func parsePositiveEnvInt(getenv func(string) string, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, errors.New(name + " must be a positive integer")
	}
	return value, nil
}

// ParseTrustedProxies parses a comma-separated list of proxy IPs or CIDRs.
// An empty value returns nil so Gin trusts no proxies and ignores forwarding headers.
func ParseTrustedProxies(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	proxies := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			return nil, errors.New("TRUSTED_PROXIES must not contain empty entries")
		}
		proxies = append(proxies, trimmed)
	}
	return proxies, nil
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

type rateDecision struct {
	allowed       bool
	limit         int
	remaining     int
	retryAfterSec int
	resetUnix     int64
}

type tokenLimiter struct {
	mu         sync.Mutex
	rate       float64 // tokens per second
	burst      float64
	limit      int
	maxEntries int
	idleTTL    time.Duration
	now        func() time.Time
	buckets    map[string]*tokenBucket
	lastSweep  time.Time
	ops        uint64
}

func newTokenLimiter(rule RateRule, maxEntries int, now func() time.Time) *tokenLimiter {
	if rule.RPM <= 0 {
		rule.RPM = 60
	}
	if rule.Burst <= 0 {
		rule.Burst = 1
	}
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	if now == nil {
		now = time.Now
	}
	rate := float64(rule.RPM) / 60.0
	refillDuration := time.Duration(math.Ceil(float64(rule.Burst)/rate)) * time.Second
	idleTTL := max(refillDuration*2, minIdleTTL)
	return &tokenLimiter{
		rate:       rate,
		burst:      float64(rule.Burst),
		limit:      rule.Burst,
		maxEntries: maxEntries,
		idleTTL:    idleTTL,
		now:        now,
		buckets:    make(map[string]*tokenBucket),
		lastSweep:  now(),
	}
}

func (l *tokenLimiter) allow(key string) rateDecision {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.ops++
	if l.ops%sweepEveryOps == 0 && now.Sub(l.lastSweep) >= time.Minute {
		l.sweepLocked(now)
	}

	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= l.maxEntries {
			if now.Sub(l.lastSweep) >= time.Second {
				l.sweepLocked(now)
			}
			if len(l.buckets) >= l.maxEntries {
				key = overflowBucketKey
				b = l.buckets[key]
			}
		}
		if b == nil {
			b = &tokenBucket{tokens: l.burst, last: now}
			l.buckets[key] = b
		}
	}

	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		resetSec := int64(math.Ceil((l.burst - b.tokens) / l.rate))
		return rateDecision{
			allowed:   true,
			limit:     l.limit,
			remaining: int(b.tokens),
			resetUnix: now.Add(time.Duration(resetSec) * time.Second).Unix(),
		}
	}

	retryAfterSec := max(1, int(math.Ceil((1.0-b.tokens)/l.rate)))
	resetSec := int64(math.Ceil((l.burst - b.tokens) / l.rate))
	return rateDecision{
		allowed:       false,
		limit:         l.limit,
		remaining:     0,
		retryAfterSec: retryAfterSec,
		resetUnix:     now.Add(time.Duration(resetSec) * time.Second).Unix(),
	}
}

func (l *tokenLimiter) sweepLocked(now time.Time) {
	for key, b := range l.buckets {
		if key == overflowBucketKey {
			continue
		}
		if now.Sub(b.last) >= l.idleTTL {
			delete(l.buckets, key)
		}
	}
	l.lastSweep = now
}

// RateLimiters provides Gin middlewares for each API traffic tier.
type RateLimiters struct {
	enabled bool
	ip      *tokenLimiter
	auth    *tokenLimiter
	api     *tokenLimiter
	chat    *tokenLimiter
}

// NewRateLimiters builds rate limiters for all tiers from cfg.
func NewRateLimiters(cfg RateLimitConfig) *RateLimiters {
	return newRateLimitersWithClock(cfg, time.Now)
}

func newRateLimitersWithClock(cfg RateLimitConfig, now func() time.Time) *RateLimiters {
	if !cfg.Enabled {
		return &RateLimiters{enabled: false}
	}
	defaults := DefaultRateLimitConfig()
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = defaults.MaxEntries
	}
	if cfg.IP.RPM <= 0 || cfg.IP.Burst <= 0 {
		cfg.IP = defaults.IP
	}
	if cfg.Auth.RPM <= 0 || cfg.Auth.Burst <= 0 {
		cfg.Auth = defaults.Auth
	}
	if cfg.API.RPM <= 0 || cfg.API.Burst <= 0 {
		cfg.API = defaults.API
	}
	if cfg.Chat.RPM <= 0 || cfg.Chat.Burst <= 0 {
		cfg.Chat = defaults.Chat
	}
	return &RateLimiters{
		enabled: true,
		ip:      newTokenLimiter(cfg.IP, cfg.MaxEntries, now),
		auth:    newTokenLimiter(cfg.Auth, cfg.MaxEntries, now),
		api:     newTokenLimiter(cfg.API, cfg.MaxEntries, now),
		chat:    newTokenLimiter(cfg.Chat, cfg.MaxEntries, now),
	}
}

// IP enforces a per-client-IP limit across API routes while skipping health probes.
func (r *RateLimiters) IP() gin.HandlerFunc {
	if r == nil || !r.enabled {
		return passThroughMiddleware
	}
	return func(c *gin.Context) {
		switch c.Request.URL.Path {
		case "/healthz", "/readyz":
			c.Next()
			return
		}
		enforceRateLimit(c, r.ip, "ip:"+clientIPKey(c))
	}
}

// Auth enforces a strict per-client-IP limit on credential endpoints.
func (r *RateLimiters) Auth() gin.HandlerFunc {
	if r == nil || !r.enabled {
		return passThroughMiddleware
	}
	return func(c *gin.Context) {
		enforceRateLimit(c, r.auth, "ip:"+clientIPKey(c))
	}
}

// API enforces a per-user limit on authenticated endpoints, falling back to client IP.
func (r *RateLimiters) API() gin.HandlerFunc {
	if r == nil || !r.enabled {
		return passThroughMiddleware
	}
	return func(c *gin.Context) {
		enforceRateLimit(c, r.api, userOrIPKey(c))
	}
}

// Chat enforces a per-user limit on chat message turns.
func (r *RateLimiters) Chat() gin.HandlerFunc {
	if r == nil || !r.enabled {
		return passThroughMiddleware
	}
	return func(c *gin.Context) {
		enforceRateLimit(c, r.chat, userOrIPKey(c))
	}
}

func passThroughMiddleware(c *gin.Context) {
	c.Next()
}

func enforceRateLimit(c *gin.Context, limiter *tokenLimiter, key string) {
	decision := limiter.allow(key)
	c.Header("X-RateLimit-Limit", strconv.Itoa(decision.limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(decision.remaining))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(decision.resetUnix, 10))
	if !decision.allowed {
		c.Header("Cache-Control", "no-store")
		c.Header("Retry-After", strconv.Itoa(decision.retryAfterSec))
		fail(c, http.StatusTooManyRequests, rateLimitedCode, rateLimitedMessage)
		return
	}
	c.Next()
}

func userOrIPKey(c *gin.Context) string {
	if id, ok := quota.UserID(c.Request.Context()); ok {
		return "user:" + strconv.FormatInt(id, 10)
	}
	return "ip:" + clientIPKey(c)
}

func clientIPKey(c *gin.Context) string {
	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		return ip
	}
	return "unknown"
}
