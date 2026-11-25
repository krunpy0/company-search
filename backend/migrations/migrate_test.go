package migrations

import (
	"context"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestApplyValidation(t *testing.T) {
	if err := Apply(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("expected missing DATABASE_URL error, got %v", err)
	}
	if err := Apply(context.Background(), "://bad-dsn"); err == nil || !strings.Contains(err.Error(), "invalid DATABASE_URL") {
		t.Fatalf("expected invalid DATABASE_URL error, got %v", err)
	}
}

func TestEmbeddedMigrationsSortedAndNonEmpty(t *testing.T) {
	names, err := fs.Glob(Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("expected embedded SQL migrations")
	}
	if !slices.IsSorted(names) {
		t.Fatalf("expected sorted migrations, got %v", names)
	}
	for _, name := range names {
		data, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}

func TestApplyPostgreSQLIdempotent(t *testing.T) {
	dsn := os.Getenv("COMPANY_MIGRATE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set COMPANY_MIGRATE_TEST_DATABASE_URL to test against PostgreSQL")
	}
	ctx := context.Background()
	if err := Apply(ctx, dsn); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := Apply(ctx, dsn); err != nil {
		t.Fatalf("second apply: %v", err)
	}
}
