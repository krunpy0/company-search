package main

import (
	"context"
	"errors"
	"testing"
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
