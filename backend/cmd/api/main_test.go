package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRunAutoMigrate(t *testing.T) {
	t.Run("enabled by default", func(t *testing.T) {
		t.Setenv("AUTO_MIGRATE", "")
		var called bool
		var gotDSN string
		err := runAutoMigrate(context.Background(), "postgres://example", func(_ context.Context, dsn string) error {
			called = true
			gotDSN = dsn
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !called || gotDSN != "postgres://example" {
			t.Fatalf("expected apply to be called with DSN, called=%v dsn=%q", called, gotDSN)
		}
	})

	t.Run("disabled with false", func(t *testing.T) {
		t.Setenv("AUTO_MIGRATE", "false")
		err := runAutoMigrate(context.Background(), "postgres://example", func(context.Context, string) error {
			t.Fatal("apply should not be called when AUTO_MIGRATE=false")
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid boolean", func(t *testing.T) {
		t.Setenv("AUTO_MIGRATE", "not-a-bool")
		err := runAutoMigrate(context.Background(), "postgres://example", func(context.Context, string) error {
			t.Fatal("apply should not be called on invalid AUTO_MIGRATE")
			return nil
		})
		if err == nil {
			t.Fatal("expected error for invalid AUTO_MIGRATE")
		}
	})

	t.Run("propagates migration failure", func(t *testing.T) {
		t.Setenv("AUTO_MIGRATE", "true")
		wantErr := errors.New("boom")
		err := runAutoMigrate(context.Background(), "postgres://example", func(context.Context, string) error {
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("got %v, want %v", err, wantErr)
		}
	})
}

func TestConfigureRateLimiting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	envMap := map[string]string{
		"RATE_LIMIT_IP_RPM":   "60",
		"RATE_LIMIT_IP_BURST": "1",
		"TRUSTED_PROXIES":     "127.0.0.1",
	}
	limiters, err := configureRateLimiting(r, func(k string) string { return envMap[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.Use(limiters.IP())
	r.GET("/v1/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	for range 3 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", "198.51.100.10")
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("healthz got %d, want 204", rec.Code)
		}
	}

	sendPing := func(forwarded string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", forwarded)
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := sendPing("198.51.100.10"); code != http.StatusOK {
		t.Fatalf("first ping got %d, want 200", code)
	}
	if code := sendPing("198.51.100.10"); code != http.StatusTooManyRequests {
		t.Fatalf("second ping for same forwarded IP got %d, want 429", code)
	}
	if code := sendPing("198.51.100.11"); code != http.StatusOK {
		t.Fatalf("different forwarded IP via trusted proxy got %d, want 200", code)
	}
}
