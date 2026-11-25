package quota

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrTokenBudget            = errors.New("daily model token budget exhausted")
	ErrTokenBudgetUnavailable = errors.New("model token budget temporarily unavailable")
)

type TokenBudget struct {
	pool  *pgxpool.Pool
	limit int64
}

func NewTokenBudget(pool *pgxpool.Pool, dailyLimit int64) TokenBudget {
	return TokenBudget{pool: pool, limit: dailyLimit}
}

type TokenReservation struct {
	pool   *pgxpool.Pool
	userID int64
	day    string
	tokens int64
}

func (b TokenBudget) Reserve(ctx context.Context, tokens int64) (TokenReservation, error) {
	userID, ok := UserID(ctx)
	if !ok || b.pool == nil || b.limit <= 0 || tokens <= 0 {
		return TokenReservation{}, ErrTokenBudgetUnavailable
	}
	if tokens > b.limit {
		return TokenReservation{}, ErrTokenBudget
	}
	reservation := TokenReservation{pool: b.pool, userID: userID, tokens: tokens}
	err := b.pool.QueryRow(ctx, `
		INSERT INTO accounts.model_token_usage AS usage (user_id, day, used)
		VALUES ($1, (statement_timestamp() AT TIME ZONE 'Europe/Moscow')::date, $2)
		ON CONFLICT (user_id, day) DO UPDATE SET used=usage.used+excluded.used
		WHERE usage.used <= $3 - excluded.used
		RETURNING day::text`, userID, tokens, b.limit).Scan(&reservation.day)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenReservation{}, ErrTokenBudget
	}
	if err != nil {
		return TokenReservation{}, ErrTokenBudgetUnavailable
	}
	return reservation, nil
}

// Reconcile replaces this call's reserve with reported usage in the original day.
func (r TokenReservation) Reconcile(ctx context.Context, actual int64) error {
	if actual == r.tokens {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		UPDATE accounts.model_token_usage SET used=used+$3
		WHERE user_id=$1 AND day=$2::text::date`, r.userID, r.day, actual-r.tokens)
	return err
}
