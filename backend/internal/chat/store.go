package chat

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/agent"
)

var (
	ErrNotFound      = errors.New("dialogue not found")
	ErrInProgress    = errors.New("dialogue has a turn in progress")
	ErrMessageBudget = errors.New("daily message budget exhausted")
	ErrSelection     = errors.New("choose a registration from the pending candidates")
)

const TurnTimeout = 2 * time.Minute
const cleanupTimeout = 3 * time.Second

type Dialogue struct {
	ID           int64     `json:"id,string"`
	Registration *string   `json:"registration,omitempty"`
	Title        *string   `json:"title,omitempty"`
	Candidates   []string  `json:"candidates,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Turn struct {
	ID        int64           `json:"id,string"`
	Question  string          `json:"question"`
	Answer    json.RawMessage `json:"answer"`
	CreatedAt time.Time       `json:"created_at"`
}

type Store struct{ Pool *pgxpool.Pool }

const dialogueColumns = "id, registration, title, pending_registrations, created_at, updated_at"
const turnColumns = "id, question, answer, created_at"

func (s Store) Create(ctx context.Context, user int64, registration *string) (Dialogue, error) {
	var d Dialogue
	err := s.Pool.QueryRow(ctx, "INSERT INTO accounts.chat_dialogues (user_id, registration) VALUES ($1, $2) RETURNING "+dialogueColumns,
		user, registration).Scan(&d.ID, &d.Registration, &d.Title, &d.Candidates, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func (s Store) List(ctx context.Context, user, before int64, limit int) ([]Dialogue, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+dialogueColumns+` FROM accounts.chat_dialogues
		WHERE user_id=$1 AND ($2=0 OR id<$2) ORDER BY id DESC LIMIT $3`, user, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Dialogue])
}

func (s Store) Search(ctx context.Context, user, before int64, limit int, query string) ([]Dialogue, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+dialogueColumns+` FROM accounts.chat_dialogues AS d
		WHERE user_id=$1 AND ($2=0 OR id<$2) AND (
			strpos(lower(d.title), lower($4)) > 0
			OR strpos(d.registration, lower($4)) > 0
			OR strpos(array_to_string(d.pending_registrations, ' '), lower($4)) > 0
			OR EXISTS (
				SELECT 1 FROM accounts.chat_turns AS t WHERE t.dialogue_id=d.id AND
				(strpos(lower(t.question), lower($4)) > 0 OR strpos(lower(t.answer->>'summary'), lower($4)) > 0)
			))
		ORDER BY id DESC LIMIT $3`, user, before, limit, query)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Dialogue])
}

func (s Store) Rename(ctx context.Context, user, id int64, title string) (Dialogue, error) {
	var d Dialogue
	err := s.Pool.QueryRow(ctx, `UPDATE accounts.chat_dialogues SET title=$3, updated_at=statement_timestamp()
		WHERE user_id=$1 AND id=$2 RETURNING `+dialogueColumns,
		user, id, title).Scan(&d.ID, &d.Registration, &d.Title, &d.Candidates, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dialogue{}, ErrNotFound
	}
	return d, err
}

func (s Store) Delete(ctx context.Context, user, id int64) error {
	deleted, err := s.Pool.Exec(ctx, "DELETE FROM accounts.chat_dialogues WHERE user_id=$1 AND id=$2", user, id)
	if err == nil && deleted.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s Store) Clear(ctx context.Context, user int64) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM accounts.chat_dialogues WHERE user_id=$1", user)
	return err
}

func (s Store) History(ctx context.Context, user, id, before int64, limit int) (Dialogue, []Turn, error) {
	var d Dialogue
	var turns []Turn
	err := pgx.BeginTxFunc(ctx, s.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "SELECT "+dialogueColumns+" FROM accounts.chat_dialogues WHERE user_id=$1 AND id=$2",
			user, id).Scan(&d.ID, &d.Registration, &d.Title, &d.Candidates, &d.CreatedAt, &d.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT "+turnColumns+` FROM accounts.chat_turns
			WHERE dialogue_id=$1 AND ($2=0 OR id<$2) ORDER BY id DESC LIMIT $3`, id, before, limit)
		if err != nil {
			return err
		}
		turns, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Turn])
		return err
	})
	return d, turns, err
}

type contextTurn struct {
	Question string
	Summary  string
}

func (s Store) recentTurns(ctx context.Context, user, id int64) ([]contextTurn, error) {
	// Do not replay previously stored provider data after a source has been disabled.
	rows, err := s.Pool.Query(ctx, `SELECT question,
		CASE WHEN EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(answer->'records','[]')) AS record
			WHERE record->>'source_ref' NOT LIKE 'fns-%')
			THEN '' ELSE answer->>'summary' END
		FROM accounts.chat_turns WHERE dialogue_id=$2
		AND EXISTS (SELECT 1 FROM accounts.chat_dialogues WHERE user_id=$1 AND id=$2)
		ORDER BY id DESC LIMIT 4`, user, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[contextTurn])
}

type claim struct {
	lease         string
	generateTitle bool

	registration *string
	question     string
}

func (s Store) claim(ctx context.Context, user, id int64, question string, titleEnabled bool) (claim, error) {
	c := claim{lease: rand.Text(), question: question}
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var busy bool
		var pending *string
		var candidates []string
		err := tx.QueryRow(ctx, `SELECT registration, pending_question, pending_registrations,
			COALESCE(lease_until > clock_timestamp(), false),
			$3 AND NOT title_attempted AND title IS NULL AND NOT EXISTS (
				SELECT 1 FROM accounts.chat_turns WHERE dialogue_id=$2)
			FROM accounts.chat_dialogues WHERE user_id=$1 AND id=$2 FOR UPDATE`, user, id, titleEnabled).
			Scan(&c.registration, &pending, &candidates, &busy, &c.generateTitle)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if busy {
			return ErrInProgress
		}
		if pending != nil {
			selected := "reg:" + strings.TrimPrefix(question, "reg:")
			if !slices.Contains(candidates, selected) {
				return ErrSelection
			}
			c.registration = &selected
			c.question = *pending
		}
		var used int
		err = tx.QueryRow(ctx, `INSERT INTO accounts.chat_message_usage AS usage (user_id, day, used)
			VALUES ($1, (clock_timestamp() AT TIME ZONE 'Europe/Moscow')::date, 1)
			ON CONFLICT (user_id, day) DO UPDATE SET used=usage.used+1 WHERE usage.used < 5
			RETURNING used`, user).Scan(&used)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMessageBudget
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE accounts.chat_dialogues SET lease_id=$3, lease_until=clock_timestamp()+interval '3 minutes',
			title_attempted=title_attempted OR $4 WHERE user_id=$1 AND id=$2`, user, id, c.lease, c.generateTitle)
		return err
	})
	return c, err
}

func (s Store) finish(ctx context.Context, user, id int64, c claim, question string, answer agent.Answer) (Turn, error) {
	var result Turn
	var pending *string
	candidates := answer.Candidates
	if len(candidates) > 0 {
		pending = &c.question
	} else {
		candidates = nil
	}
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE accounts.chat_dialogues SET registration=$4, pending_question=$5,
			pending_registrations=$6, lease_id=NULL, lease_until=NULL, updated_at=statement_timestamp()
			WHERE user_id=$1 AND id=$2 AND lease_id=$3`, user, id, c.lease, c.registration, pending, candidates)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrInProgress
		}
		return tx.QueryRow(ctx, `INSERT INTO accounts.chat_turns (dialogue_id, question, answer)
			VALUES ($1, $2, $3) RETURNING `+turnColumns, id, question, answer).
			Scan(&result.ID, &result.Question, &result.Answer, &result.CreatedAt)
	})
	return result, err
}

func (s Store) release(ctx context.Context, user, id int64, c claim) {
	_, _ = s.Pool.Exec(ctx, `UPDATE accounts.chat_dialogues SET lease_id=NULL, lease_until=NULL
		WHERE user_id=$1 AND id=$2 AND lease_id=$3`, user, id, c.lease)
}
