package httpapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func MaintainSessions(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		batchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := deleteExpiredSessions(batchCtx, pool, 1000)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("expired session cleanup failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func deleteExpiredSessions(ctx context.Context, pool *pgxpool.Pool, limit int) error {
	_, err := pool.Exec(ctx, `WITH cleanup_lock AS MATERIALIZED (
		SELECT pg_try_advisory_xact_lock(hashtext('company-search'), hashtext('expired-sessions')) AS acquired
	)
	DELETE FROM accounts.sessions WHERE token_hash IN (
		SELECT token_hash FROM accounts.sessions
		WHERE expires_at <= statement_timestamp() AND (SELECT acquired FROM cleanup_lock)
		ORDER BY expires_at LIMIT $1
	)`, limit)
	return err
}
