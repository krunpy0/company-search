package migrations

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

const advisoryLockID = 71001

// Apply executes all embedded SQL migrations that have not yet been recorded in schema_migrations.
func Apply(ctx context.Context, dsn string) error {
	return applyFS(ctx, dsn, Files)
}

func applyFS(ctx context.Context, dsn string, fsys fs.FS) error {
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("invalid DATABASE_URL")
	}
	migrateCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	conn, err := pgx.ConnectConfig(migrateCtx, config)
	if err != nil {
		return fmt.Errorf("could not connect to PostgreSQL")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_ = conn.Close(cleanup)
	}()

	tx, err := conn.Begin(migrateCtx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()

	if _, err := tx.Exec(migrateCtx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockID); err != nil {
		return err
	}
	if _, err := tx.Exec(migrateCtx, `CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY)`); err != nil {
		return err
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	slices.Sort(names)
	for _, name := range names {
		var applied bool
		if err := tx.QueryRow(migrateCtx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name=$1)", name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrateCtx, string(sql)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(migrateCtx, "INSERT INTO schema_migrations VALUES ($1)", name); err != nil {
			return err
		}
		slog.Info("applied migration", "name", name)
	}
	return tx.Commit(migrateCtx)
}
