package company

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSearchLookupLeavesConnectionAvailable(t *testing.T) {
	dsn := os.Getenv("COMPANY_ACCOUNTS_TEST_API_URL")
	if dsn == "" {
		t.Skip("run test-postgres.ps1 for an isolated database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	calls := 0
	reader := Reader{Pool: pool, Lookup: func(ctx context.Context, _, _, _ string) ([]RegistryParty, bool, string) {
		calls++
		if err := pool.Ping(ctx); err != nil {
			t.Fatalf("the database connection is unavailable during lookup: %v", err)
		}
		return []RegistryParty{{INN: "3907056353", Registration: "1073906000376", Kind: "legal"}}, false, ""
	}}
	result, err := reader.Read(t.Context(), ReadSearch, "", url.Values{"q": {"Пример"}})
	if err != nil || result.Error != nil || calls != 1 {
		t.Fatalf("search error=%v, rejection=%v, lookup calls=%d", err, result.Error, calls)
	}

	t.Run("local failure skips lookup", func(t *testing.T) {
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		reader.Lookup = func(context.Context, string, string, string) ([]RegistryParty, bool, string) {
			t.Error("lookup ran before local search succeeded")
			return nil, false, ""
		}
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		if _, err := reader.Read(ctx, ReadSearch, "", url.Values{"q": {"Пример"}}); err == nil {
			t.Fatal("expected local search to fail with no available connection")
		}
	})
}
