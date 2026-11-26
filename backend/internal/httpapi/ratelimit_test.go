package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/krunpy0/company-search/backend/internal/quota"
)

func TestTokenLimiterBurstAndRefill(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	limiter := newTokenLimiter(RateRule{RPM: 60, Burst: 3}, 16, func() time.Time { return now })

	for wantRem := 2; wantRem >= 0; wantRem-- {
		d := limiter.allow("ip:198.51.100.1")
		if !d.allowed || d.limit != 3 || d.remaining != wantRem {
			t.Fatalf("unexpected decision at remaining=%d: %+v", wantRem, d)
		}
	}

	denied := limiter.allow("ip:198.51.100.1")
	if denied.allowed || denied.remaining != 0 || denied.retryAfterSec != 1 {
		t.Fatalf("expected 429 decision with Retry-After=1, got %+v", denied)
	}

	// Other keys have independent buckets.
	other := limiter.allow("ip:198.51.100.2")
	if !other.allowed || other.remaining != 2 {
		t.Fatalf("expected independent bucket for second key, got %+v", other)
	}

	// Clock jump backwards must not remove tokens or break refill.
	now = now.Add(-5 * time.Second)
	if d := limiter.allow("ip:198.51.100.1"); d.allowed {
		t.Fatalf("expected denied after backwards clock jump, got %+v", d)
	}

	// Advance 2 seconds from last token timestamp -> +2 tokens at 60 RPM (1 req/sec).
	now = now.Add(7 * time.Second)
	refilled := limiter.allow("ip:198.51.100.1")
	if !refilled.allowed || refilled.remaining != 1 {
		t.Fatalf("expected 2 refilled tokens (1 remaining after allow), got %+v", refilled)
	}

	// Advance a long time -> capped at Burst.
	now = now.Add(time.Hour)
	capped := limiter.allow("ip:198.51.100.1")
	if !capped.allowed || capped.remaining != 2 {
		t.Fatalf("expected tokens capped at burst=3 (2 remaining), got %+v", capped)
	}
}

func TestTokenLimiterOverflowAndSweep(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	limiter := newTokenLimiter(RateRule{RPM: 60, Burst: 2}, 3, func() time.Time { return now })

	for i := range 3 {
		if d := limiter.allow("ip:10.0.0." + strconv.Itoa(i)); !d.allowed {
			t.Fatalf("expected key %d to be allowed", i)
		}
	}
	// New keys beyond maxEntries share overflow bucket without growing map unboundedly.
	if d := limiter.allow("ip:10.0.0.99"); !d.allowed {
		t.Fatal("expected first overflow request allowed")
	}
	if d := limiter.allow("ip:10.0.0.100"); !d.allowed {
		t.Fatal("expected second overflow request allowed")
	}
	if d := limiter.allow("ip:10.0.0.101"); d.allowed {
		t.Fatal("expected third overflow request within burst=2 to be rate limited")
	}
	if len(limiter.buckets) > 4 {
		t.Fatalf("expected bounded buckets (<= maxEntries+1), got %d", len(limiter.buckets))
	}

	// Advance past idleTTL so sweep reclaims stale keys.
	now = now.Add(minIdleTTL + time.Minute)
	if d := limiter.allow("ip:10.0.0.200"); !d.allowed {
		t.Fatal("expected new key allowed after idleTTL sweep")
	}
	if _, exists := limiter.buckets["ip:10.0.0.0"]; exists {
		t.Fatal("expected expired key to be evicted")
	}
}

func TestRateLimitMiddlewaresHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	cfg := RateLimitConfig{
		Enabled:    true,
		MaxEntries: 100,
		IP:         RateRule{RPM: 60, Burst: 6},
		Auth:       RateRule{RPM: 12, Burst: 2},
		API:        RateRule{RPM: 60, Burst: 3},
		Chat:       RateRule{RPM: 30, Burst: 2},
	}
	limiters := newRateLimitersWithClock(cfg, func() time.Time { return now })

	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	var authCalls, sessionChecks atomic.Int32
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/readyz", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.Use(limiters.IP())
	r.POST("/v1/auth/login", limiters.Auth(), func(c *gin.Context) {
		authCalls.Add(1)
		c.Status(http.StatusOK)
	})
	authSession := func(c *gin.Context) {
		sessionChecks.Add(1)
		raw := c.GetHeader("X-Test-User")
		if raw == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.ParseInt(raw, 10, 64)
		c.Request = c.Request.WithContext(quota.WithUser(c.Request.Context(), id))
		c.Next()
	}
	r.GET("/v1/companies", authSession, limiters.API(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.POST("/v1/chat/dialogues/1/messages", authSession, limiters.API(), limiters.Chat(), func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	call := func(method, path, remoteAddr, userID, xff string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = remoteAddr
		if userID != "" {
			req.Header.Set("X-Test-User", userID)
		}
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	assertLimited := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" || rec.Header().Get("X-RateLimit-Remaining") != "0" ||
			rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Request-ID") == "" {
			t.Fatalf("missing rate limit headers: %+v", rec.Header())
		}
		var payload struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				RequestID string `json:"request_id"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Error.Code != "rate_limited" || payload.Error.RequestID == "" {
			t.Fatalf("unexpected error body: %s", rec.Body.String())
		}
	}

	// Health checks are never rate-limited.
	for range 20 {
		if rec := call(http.MethodGet, "/healthz", "203.0.113.10:1000", "", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("healthz unexpectedly rejected: %d", rec.Code)
		}
		if rec := call(http.MethodGet, "/readyz", "203.0.113.10:1000", "", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("readyz unexpectedly rejected: %d", rec.Code)
		}
	}

	// Auth tier allows burst=2 per IP; X-Forwarded-For spoofing is ignored when TrustedProxies is nil.
	if rec := call(http.MethodPost, "/v1/auth/login", "203.0.113.10:1001", "", "1.1.1.1"); rec.Code != http.StatusOK {
		t.Fatalf("first login failed: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/v1/auth/login", "203.0.113.10:1002", "", "2.2.2.2"); rec.Code != http.StatusOK {
		t.Fatalf("second login failed: %d", rec.Code)
	}
	assertLimited(call(http.MethodPost, "/v1/auth/login", "203.0.113.10:1003", "", "3.3.3.3"))
	if authCalls.Load() != 2 {
		t.Fatalf("rate-limited login reached handler: calls=%d", authCalls.Load())
	}

	// API tier keys by user_id independently even from the same IP.
	for range 3 {
		if rec := call(http.MethodGet, "/v1/companies", "198.51.100.5:2000", "42", ""); rec.Code != http.StatusOK {
			t.Fatalf("user 42 request failed: %d", rec.Code)
		}
	}
	assertLimited(call(http.MethodGet, "/v1/companies", "198.51.100.5:2000", "42", ""))
	if rec := call(http.MethodGet, "/v1/companies", "198.51.100.5:2000", "43", ""); rec.Code != http.StatusOK {
		t.Fatalf("different user on same IP should not be rate limited: %d", rec.Code)
	}

	// Chat tier limits message turns stricter than general API tier.
	if rec := call(http.MethodPost, "/v1/chat/dialogues/1/messages", "198.51.100.7:3000", "99", ""); rec.Code != http.StatusCreated {
		t.Fatalf("first chat failed: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/v1/chat/dialogues/1/messages", "198.51.100.7:3000", "99", ""); rec.Code != http.StatusCreated {
		t.Fatalf("second chat failed: %d", rec.Code)
	}
	assertLimited(call(http.MethodPost, "/v1/chat/dialogues/1/messages", "198.51.100.7:3000", "99", ""))

	// IP tier blocks pre-auth floods before hitting authSession.
	before := sessionChecks.Load()
	for range 6 {
		if rec := call(http.MethodGet, "/v1/companies", "198.51.100.50:4000", "", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status before IP burst exhausted: %d", rec.Code)
		}
	}
	assertLimited(call(http.MethodGet, "/v1/companies", "198.51.100.50:4000", "", ""))
	if got := sessionChecks.Load() - before; got != 6 {
		t.Fatalf("expected IP limiter to block before session middleware, sessionChecks=%d", got)
	}
}

func TestRateLimitDisabledAndConcurrent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	disabled := NewRateLimiters(RateLimitConfig{Enabled: false})
	r := gin.New()
	r.Use(disabled.IP(), disabled.Auth(), disabled.API(), disabled.Chat())
	r.GET("/v1/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	for range 20 {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/ping", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected disabled limiter to allow request, got %d", rec.Code)
		}
	}

	concurrent := NewRateLimiters(RateLimitConfig{
		Enabled:    true,
		MaxEntries: 100,
		IP:         RateRule{RPM: 60, Burst: 10},
		Auth:       RateRule{RPM: 60, Burst: 10},
		API:        RateRule{RPM: 60, Burst: 10},
		Chat:       RateRule{RPM: 60, Burst: 10},
	})
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 25 {
		wg.Go(func() {
			if concurrent.api.allow("user:1").allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("expected exactly 10 concurrent requests allowed for burst=10, got %d", allowed.Load())
	}
}

func TestRateLimitConfigFromEnvAndTrustedProxies(t *testing.T) {
	defaults, err := RateLimitConfigFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error on defaults: %v", err)
	}
	if !defaults.Enabled || defaults.Auth.RPM != 10 || defaults.Auth.Burst != 15 ||
		defaults.IP.RPM != 240 || defaults.API.RPM != 120 || defaults.Chat.RPM != 20 {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}

	customEnv := map[string]string{
		"RATE_LIMIT_ENABLED":     "false",
		"RATE_LIMIT_MAX_ENTRIES": "2048",
		"RATE_LIMIT_IP_RPM":      "300",
		"RATE_LIMIT_IP_BURST":    "150",
		"RATE_LIMIT_AUTH_RPM":    "5",
		"RATE_LIMIT_AUTH_BURST":  "8",
		"RATE_LIMIT_API_RPM":     "90",
		"RATE_LIMIT_API_BURST":   "45",
		"RATE_LIMIT_CHAT_RPM":    "12",
		"RATE_LIMIT_CHAT_BURST":  "6",
	}
	custom, err := RateLimitConfigFromEnv(func(k string) string { return customEnv[k] })
	if err != nil {
		t.Fatalf("unexpected error on custom config: %v", err)
	}
	if custom.Enabled || custom.MaxEntries != 2048 || custom.Auth != (RateRule{RPM: 5, Burst: 8}) ||
		custom.IP != (RateRule{RPM: 300, Burst: 150}) || custom.API != (RateRule{RPM: 90, Burst: 45}) ||
		custom.Chat != (RateRule{RPM: 12, Burst: 6}) {
		t.Fatalf("unexpected custom config: %+v", custom)
	}

	for _, bad := range []map[string]string{
		{"RATE_LIMIT_ENABLED": "maybe"},
		{"RATE_LIMIT_AUTH_RPM": "0"},
		{"RATE_LIMIT_AUTH_BURST": "-1"},
		{"RATE_LIMIT_API_RPM": "abc"},
		{"RATE_LIMIT_CHAT_BURST": "0"},
		{"RATE_LIMIT_MAX_ENTRIES": "0"},
	} {
		if _, err := RateLimitConfigFromEnv(func(k string) string { return bad[k] }); err == nil {
			t.Fatalf("expected error for invalid env %+v", bad)
		}
	}

	if proxies, err := ParseTrustedProxies(""); err != nil || proxies != nil {
		t.Fatalf("expected nil proxies for empty env, got %v, %v", proxies, err)
	}
	if proxies, err := ParseTrustedProxies("127.0.0.1, ::1/128"); err != nil || len(proxies) != 2 || proxies[1] != "::1/128" {
		t.Fatalf("unexpected parsed proxies: %v, %v", proxies, err)
	}
	if _, err := ParseTrustedProxies("127.0.0.1, "); err == nil {
		t.Fatal("expected error for empty proxy entry")
	}
}
