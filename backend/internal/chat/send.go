package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/krunpy0/company-search/backend/internal/agent"
	"github.com/krunpy0/company-search/backend/internal/quota"
)

var ErrModelUnavailable = errors.New("model temporarily unavailable")

type Service struct {
	Store
	Run           agent.Run
	GenerateTitle func(context.Context, string) (string, error)
}

func (s Service) Send(ctx context.Context, user, id int64, question string) (Turn, error) {
	if s.Run.Provider == nil {
		// Confirm ownership before exposing chat availability.
		if _, _, err := s.History(ctx, user, id, 0, 1); err != nil {
			return Turn{}, err
		}
		return Turn{}, ErrModelUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, TurnTimeout)
	defer cancel()
	c, err := s.claim(ctx, user, id, question, s.GenerateTitle != nil)
	if err != nil {
		return Turn{}, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer stop()
		s.release(cleanup, user, id, c)
	}()
	if c.generateTitle {
		go s.nameDialogue(context.WithoutCancel(ctx), user, id, c.question)
	}
	turns, err := s.recentTurns(ctx, user, id)
	if err != nil {
		return Turn{}, err
	}
	history := priorMessages(turns)
	run := s.Run
	provider := &turnProvider{Provider: run.Provider}
	run.Provider = provider
	selected := ""
	if c.registration != nil {
		selected = *c.registration
	}
	answer, err := run.AskWithHistory(ctx, c.question, selected, history)
	if errors.Is(provider.err, quota.ErrTokenBudget) || errors.Is(provider.err, quota.ErrTokenBudgetUnavailable) {
		return Turn{}, provider.err
	}
	if provider.err != nil || err != nil {
		return Turn{}, fmt.Errorf("%w: %v", ErrModelUnavailable, errors.Join(provider.err, err))
	}
	saveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer stop()
	return s.finish(saveCtx, user, id, c, question, answer)
}

func (s Service) nameDialogue(ctx context.Context, user, id int64, question string) {
	modelCtx, cancel := context.WithTimeoutCause(ctx, 45*time.Second, errors.New("chat title model timed out after 45 seconds"))
	title, err := s.GenerateTitle(modelCtx, question)
	cancel()
	if err != nil {
		slog.Warn("chat title generation failed", "dialogue_id", id, "error", err)
		return
	}
	if title == "" {
		slog.Warn("chat title generation failed", "dialogue_id", id, "error", "model returned an empty title")
		return
	}
	saveCtx, stop := context.WithTimeout(ctx, cleanupTimeout)
	defer stop()
	if _, err := s.Pool.Exec(saveCtx, `UPDATE accounts.chat_dialogues SET title=$3
		WHERE user_id=$1 AND id=$2 AND title IS NULL`, user, id, title); err != nil {
		var dbErr *pgconn.PgError
		switch {
		case errors.As(err, &dbErr):
			slog.Warn("chat title save failed", "dialogue_id", id, "sqlstate", dbErr.Code)
		case saveCtx.Err() != nil:
			slog.Warn("chat title save failed", "dialogue_id", id, "error", saveCtx.Err())
		default:
			slog.Warn("chat title save failed", "dialogue_id", id, "error", "database unavailable")
		}
	}
}

func priorMessages(turns []contextTurn) []agent.Message {
	const maxBytes = 6000
	remaining := maxBytes
	history := make([]agent.Message, 0, 2*len(turns))
	for _, turn := range turns {
		remaining -= len(turn.Question)
		if remaining < 0 {
			break
		}
		if turn.Summary != "" && len(turn.Summary) <= remaining {
			history = append(history, agent.Message{Role: "assistant", Content: turn.Summary})
			remaining -= len(turn.Summary)
		}
		history = append(history, agent.Message{Role: "user", Content: turn.Question})
	}
	slices.Reverse(history)
	return history
}

// The CLI may return partial evidence after a provider error. HTTP returns a distinct error.
type turnProvider struct {
	agent.Provider
	err error
}

func (p *turnProvider) Complete(ctx context.Context, messages []agent.Message, schemas json.RawMessage) (agent.Message, error) {
	reply, err := p.Provider.Complete(ctx, messages, schemas)
	p.err = err
	return reply, err
}
