package quota

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type userKey struct{}

func WithUser(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userKey{}, userID)
}

func UserID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(userKey{}).(int64)
	return id, ok && id > 0
}

type Gate struct {
	pool                     *pgxpool.Pool
	key                      [sha256.Size]byte
	providerLimit, userLimit int
}

func New(pool *pgxpool.Pool, provider, credential string, providerLimit, userLimit int) Gate {
	return Gate{
		pool: pool, key: sha256.Sum256([]byte(provider + "\x00" + credential)),
		providerLimit: providerLimit, userLimit: userLimit,
	}
}

func (g Gate) Reserve(ctx context.Context) string {
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return "provider_unavailable"
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err := tx.Exec(ctx, `
		INSERT INTO accounts.external_credentials (key_hash, day)
		VALUES ($1, (clock_timestamp() AT TIME ZONE 'Europe/Moscow')::date)
		ON CONFLICT DO NOTHING`, g.key[:]); err != nil {
		return "provider_unavailable"
	}
	// Lock before reading the clock so waiting for another instance cannot use a stale day.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM accounts.external_credentials WHERE key_hash=$1 FOR UPDATE`, g.key[:]); err != nil {
		return "provider_unavailable"
	}

	var now time.Time
	var day string
	var used int
	var cooling, waiting bool
	err = tx.QueryRow(ctx, `
		WITH stamp AS (
			SELECT clock_timestamp() AS now
		), bucket AS (
			SELECT now, (now AT TIME ZONE 'Europe/Moscow')::date AS today FROM stamp
		)
		SELECT now, today::text, CASE WHEN day=today THEN used ELSE 0 END,
		       now < cooldown_until, now < next_at
		FROM accounts.external_credentials CROSS JOIN bucket WHERE key_hash=$1`, g.key[:]).
		Scan(&now, &day, &used, &cooling, &waiting)
	if err != nil {
		return "provider_unavailable"
	}
	switch {
	case cooling:
		return "provider_rate_limited"
	case used >= g.providerLimit:
		return "provider_budget_exhausted"
	case waiting:
		return "provider_rate_limited"
	}

	if userID, ok := UserID(ctx); ok {
		var reserved int
		err := tx.QueryRow(ctx, `
			INSERT INTO accounts.external_usage AS usage (user_id, day, used)
			VALUES ($1, $2::text::date, 1)
			ON CONFLICT (user_id, day) DO UPDATE SET used=usage.used+1
			WHERE usage.used < $3
			RETURNING used`, userID, day, g.userLimit).Scan(&reserved)
		if errors.Is(err, pgx.ErrNoRows) {
			return "user_external_api_budget_exhausted"
		}
		if err != nil {
			return "provider_unavailable"
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE accounts.external_credentials SET day=$2::text::date, used=$3, next_at=$4
		WHERE key_hash=$1`, g.key[:], day, used+1, now.Add(time.Second)); err != nil {
		return "provider_unavailable"
	}
	if err := tx.Commit(ctx); err != nil {
		return "provider_unavailable"
	}
	return ""
}

func (g Gate) Backoff(ctx context.Context, until time.Time) error {
	_, err := g.pool.Exec(ctx, `
		UPDATE accounts.external_credentials SET cooldown_until=greatest(cooldown_until, $2)
		WHERE key_hash=$1`, g.key[:], until)
	return err
}
