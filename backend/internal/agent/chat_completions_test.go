package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/quota"
)

func TestChatCompletionsToolRoundTrip(t *testing.T) {
	const schemas = `{"get_company_profile":{"description":"Company profile","type":"object","required":["id"]}}`
	const details = `[{"type":"reasoning.encrypted","data":"opaque","index":0}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/custom/v1/chat/completions" ||
			r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("Chat Completions request method, URL or headers differ from the chat API")
		}
		var request struct {
			Model     string          `json:"model"`
			Messages  []Message       `json:"messages"`
			Tools     json.RawMessage `json:"tools"`
			MaxTokens int             `json:"max_tokens"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		const wireTools = `[{"type":"function","function":{"name":"get_company_profile","description":"Company profile","parameters":{"description":"Company profile","type":"object","required":["id"]}}}]`
		if string(request.Tools) != wireTools || request.MaxTokens <= 0 {
			t.Error("expected the generated tool schema, token cap")
		}
		if len(request.Messages) == 1 {
			if request.Model != "test/model" || request.Messages[0].Content != "Карточка" {
				t.Errorf("unexpected first request: %+v", request)
			}
			io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning_details":`+details+`,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_company_profile","arguments":"{\"id\":\"reg:1073906000376\"}"}}]}}]}`)
			return
		}
		if request.Model != "test/model" || len(request.Messages) != 3 ||
			string(request.Messages[1].ReasoningDetails) != details ||
			request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "call_1" ||
			request.Messages[2].Content != `{"data":{"id":"reg:1073906000376"}}` {
			t.Errorf("unexpected follow-up request: %+v", request)
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"facts\":[]}"}}]}`)
	}))
	defer server.Close()

	ctx := context.Background()
	p, err := NewChatCompletions(server.URL+"/custom/v1/", "test/model", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	messages := []Message{{Role: "user", Content: "Карточка"}}
	assistant, err := p.Complete(ctx, messages, json.RawMessage(schemas))
	if err != nil || len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v, %v", assistant, err)
	}
	call := assistant.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "get_company_profile" || call.Function.Arguments != `{"id":"reg:1073906000376"}` {
		t.Fatalf("unexpected tool call: %+v", call)
	}
	messages = append(messages, assistant, Message{
		Role: "tool", ToolCallID: call.ID, Content: `{"data":{"id":"reg:1073906000376"}}`,
	})

	final, err := p.Complete(ctx, messages, json.RawMessage(schemas))
	if err != nil || final.Content != `{"facts":[]}` {
		t.Fatalf("expected final content, got %+v, %v", final, err)
	}
}

func TestChatCompletionsRejectsIncompleteResponse(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"upstream error", 429, "do not expose upstream response"},
		{"oversized", 200, strings.Repeat("x", (1<<20)+1)},
		{"truncated generation", 200, `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"partial"}}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("unexpected Authorization header")
				}
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer server.Close()
			p, err := NewChatCompletions(server.URL, "local-model", "")
			if err != nil {
				t.Fatal(err)
			}
			message, err := p.Complete(context.Background(), []Message{{Role: "user", Content: "test"}}, json.RawMessage(`{"lookup":{"type":"object"}}`))
			if err == nil || strings.Contains(err.Error(), tt.body) || message.Content != "" {
				t.Fatalf("expected a sanitized error and empty message, got %+v, %v", message, err)
			}
		})
	}
}

func TestChatCompletionsTokenBudget(t *testing.T) {
	dsn := os.Getenv("COMPANY_CHAT_TEST_API_URL")
	if dsn == "" {
		t.Skip("set COMPANY_CHAT_TEST_API_URL to a migrated stage15_test database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var database string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "stage15_test") {
		t.Fatalf("use a dedicated stage15_test database: %v", err)
	}
	newUser := func() int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO accounts.users(email,password_hash)
			VALUES ($1, $2) RETURNING id`, rand.Text()+"@example.test", []byte("synthetic")).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second := newUser(), newUser()
	currentUsage := func(ctx context.Context, id int64) (int64, error) {
		var spent int64
		err := pool.QueryRow(ctx, `SELECT used FROM accounts.model_token_usage
			WHERE user_id=$1 AND day=(statement_timestamp() AT TIME ZONE 'Europe/Moscow')::date`, id).Scan(&spent)
		return spent, err
	}
	const dailyLimit int64 = 4000
	var calls atomic.Int32
	reservedAtNetwork := make(chan int64, 1)
	proceed := make(chan struct{})
	release := sync.OnceFunc(func() { close(proceed) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const valid = `"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"synthetic"}}]`
		switch calls.Add(1) {
		case 1:
			spent, err := currentUsage(r.Context(), first)
			if err != nil {
				t.Error(err)
			}
			reservedAtNetwork <- spent
			select {
			case <-proceed:
			case <-r.Context().Done():
				return
			}
			io.WriteString(w, `{`+valid+`,"usage":{"prompt_tokens":7,"completion_tokens":4,"total_tokens":11}}`)
		case 2:
			io.WriteString(w, `{`+valid+`}`)
		case 3:
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"usage":{"total_tokens":5000}}`)
		default:
			t.Error("a model call escaped the token budget")
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	defer release()
	p, err := NewChatCompletions(server.URL, "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	p = p.WithTokenBudget(quota.NewTokenBudget(pool, dailyLimit))
	complete := func(ctx context.Context) error {
		_, err := p.Complete(ctx, []Message{{Role: "user", Content: "Карточка"}}, json.RawMessage(`{"lookup":{"type":"object"}}`))
		return err
	}
	if err := complete(ctx); !errors.Is(err, quota.ErrTokenBudgetUnavailable) || calls.Load() != 0 {
		t.Fatalf("missing user must fail closed, got %v after %d model calls", err, calls.Load())
	}
	observed := make(chan TokenUsage, 1)
	firstCtx := quota.WithUser(ctx, first)
	done := make(chan error, 1)
	go func() {
		done <- complete(WithTokenUsage(firstCtx, func(usage TokenUsage) { observed <- usage }))
	}()
	var reserved int64
	select {
	case reserved = <-reservedAtNetwork:
	case err := <-done:
		t.Fatalf("reservation failed before the request: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if reserved <= dailyLimit/2 || reserved > dailyLimit {
		t.Fatalf("unexpected reservation at network boundary: %d", reserved)
	}
	if err := complete(firstCtx); !errors.Is(err, quota.ErrTokenBudget) || calls.Load() != 1 {
		t.Fatalf("parallel request must stop at quota, got %v after %d model calls", err, calls.Load())
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case usage := <-observed:
		if usage != (TokenUsage{PromptTokens: 7, CompletionTokens: 4, TotalTokens: 11, ReservedTokens: reserved}) {
			t.Fatalf("unexpected usage: %+v", usage)
		}
	default:
		t.Fatal("usage not observed")
	}
	checkUsage := func(id, want int64) {
		t.Helper()
		if got, err := currentUsage(ctx, id); err != nil || got != want {
			t.Fatalf("usage = %d, want %d, err %v", got, want, err)
		}
	}
	checkUsage(first, 11)
	if err := complete(firstCtx); err != nil {
		t.Fatal(err)
	}
	checkUsage(first, 11+reserved)
	secondCtx := quota.WithUser(ctx, second)
	if err := complete(secondCtx); err == nil || errors.Is(err, quota.ErrTokenBudget) {
		t.Fatalf("second user must reach the provider, got %v", err)
	}
	checkUsage(second, 5000)
	if err := complete(secondCtx); !errors.Is(err, quota.ErrTokenBudget) || calls.Load() != 3 {
		t.Fatalf("reported overage must stop the next call, got %v after %d model calls", err, calls.Load())
	}
}
