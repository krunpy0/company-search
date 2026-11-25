package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/krunpy0/company-search/backend/migrations"
)

func main() {
	if err := migrations.Apply(context.Background(), os.Getenv("DATABASE_URL")); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}
