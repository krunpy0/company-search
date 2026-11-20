package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/krunpy0/company-search/backend/migrations"
)

func main() {
	if err := migrate(); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

func migrate() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("invalid DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("could not connect to PostgreSQL")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(71001);
		CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY)`); err != nil {
		return err
	}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return err
	}
	for _, name := range names {
		var applied bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name=$1)", name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := migrations.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations VALUES ($1)", name); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
