package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/agent"
	"github.com/krunpy0/company-search/backend/internal/chat"
	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/quota"
	"github.com/krunpy0/company-search/backend/internal/tools"
)

func TestChatHTTP(t *testing.T) {
	adminDSN := os.Getenv("COMPANY_CHAT_TEST_ADMIN_URL")
	apiDSN := os.Getenv("COMPANY_CHAT_TEST_API_URL")
	if adminDSN == "" || apiDSN == "" {
		t.Skip("set COMPANY_CHAT_TEST_ADMIN_URL and COMPANY_CHAT_TEST_API_URL")
	}
	ctx := t.Context()
	pool := func(dsn string) *pgxpool.Pool {
		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	admin, api, secondAPI := pool(adminDSN), pool(apiDSN), pool(apiDSN)
	var database string
	if err := admin.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "stage15_test") {
		t.Fatalf("use a dedicated stage15_test database: %v", err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The database is otherwise empty and dedicated to this test suite.
	exec("TRUNCATE public.datasets, public.registrations CASCADE")
	fixture, err := os.ReadFile("../tools/testdata/stage10.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(fixture))
	exec(`INSERT INTO tax_debts SELECT release_id,'3907056353',tax_name,principal,penalties,fines,total,as_of,document_date,source_doc_id,source_file,line_no
		FROM tax_debts WHERE inn='6612025806'`)
	exec(`INSERT INTO tax_payments SELECT release_id,'3907056353',tax_name,amount,as_of,document_date,source_doc_id,source_file,line_no
		FROM tax_payments WHERE inn='7838080688'`)

	var failModel, failRegistry atomic.Bool
	blocked := make(chan struct{})
	resume := make(chan struct{})
	var observedFollowup atomic.Bool
	var titleCalls atomic.Int32
	mainStarted, allowMain := make(chan struct{}), make(chan struct{})
	var startMain sync.Once
	refPattern := regexp.MustCompile(`"(?:fns|dadata)-[0-9]+"`)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failModel.Load() {
			w.WriteHeader(503)
			return
		}
		var req struct {
			Messages []agent.Message
			Tools    json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		last := req.Messages[len(req.Messages)-1]
		if len(req.Tools) == 0 {
			titleCalls.Add(1)
			if last.Content == "Задолженность" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"«Налоговая задолженность»"}}],"usage":{"total_tokens":30}}`))
				return
			}
			w.WriteHeader(503)
			return
		}
		reply := agent.Message{Role: "assistant"}
		finish := "stop"
		if last.Role == "tool" {
			var refs []string
			for _, msg := range req.Messages {
				if msg.Role != "tool" {
					continue
				}
				for _, ref := range refPattern.FindAllString(msg.Content, -1) {
					refs = append(refs, "["+strings.Trim(ref, `"`)+"]")
				}
			}
			reply.Content = "Данные проверены. " + strings.Join(refs, " ")
		} else {
			reg := "reg:1073906000376"
			if strings.Contains(req.Messages[0].Content, "reg:326000000000048") {
				reg = "reg:326000000000048"
			}
			tool, args := "get_company_profile", `{"id":"`+reg+`"}`
			switch last.Content {
			case "Неоднозначный ИНН":
				if reg != "reg:326000000000048" {
					tool, args = "search_companies", `{"q":"500100732259","mode":"inn"}`
				}
			case "Выдумай сумму":
				reply.Content = "Долг 999999 рублей."
				break
			case "Задолженность":
				startMain.Do(func() { close(mainStarted) })
				select {
				case <-allowMain:
				case <-r.Context().Done():
					return
				}
				tool, args = "get_company_taxes", `{"id":"`+reg+`","section":"debts"}`
			case "А уплаченные?":
				var hasQuestion, hasAnswer bool
				for _, msg := range req.Messages[:len(req.Messages)-1] {
					hasQuestion = hasQuestion || msg.Role == "user" && msg.Content == "Задолженность"
					hasAnswer = hasAnswer || msg.Role == "assistant" && strings.Contains(msg.Content, "Данные проверены.")
				}
				observedFollowup.Store(hasQuestion && hasAnswer)
				tool, args = "get_company_taxes", `{"id":"`+reg+`","section":"payments"}`
			case "Профиль и налоги":
				reply.ToolCalls = []agent.ToolCall{{ID: "profile", Type: "function", Function: agent.FunctionCall{Name: tool, Arguments: args}}}
				tool, args = "get_company_taxes", `{"id":"`+reg+`","section":"payments"}`
			case "Блокировка":
				close(blocked)
				<-resume
			}
			if reply.Content == "" {
				finish = "tool_calls"
				reply.ToolCalls = append(reply.ToolCalls, agent.ToolCall{ID: "read", Type: "function", Function: agent.FunctionCall{Name: tool, Arguments: args}})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": reply, "finish_reason": finish}},
			"usage":   map[string]int{"prompt_tokens": 25, "completion_tokens": 10, "total_tokens": 35},
		})
	}))
	t.Cleanup(mock.Close)
	provider, err := agent.NewChatCompletions(mock.URL, "mock", "")
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(ctx context.Context, query, mode, kind string) ([]company.RegistryParty, bool, string) {
		if failRegistry.Load() {
			return nil, false, "provider_unavailable"
		}
		if query == "1073906000376" && mode == "registration" {
			return []company.RegistryParty{{INN: "3907056353", Registration: query, Kind: "legal", Name: "ООО Тестовый реестр", Status: "ACTIVE"}}, false, ""
		}
		return nil, false, ""
	}
	router := func(p *pgxpool.Pool, budget int64) *gin.Engine {
		r := gin.New()
		session := RegisterAuth(r, p)
		RegisterChat(r.Group("", session), chat.Service{Store: chat.Store{Pool: p}, GenerateTitle: provider.WithTokenBudget(quota.NewTokenBudget(p, budget)).ChatTitle, Run: agent.Run{
			Provider: provider.WithTokenBudget(quota.NewTokenBudget(p, budget)),
			Tools:    tools.Executor{Reader: company.Reader{Pool: p, Lookup: lookup}},
			Steps:    6, Bytes: 16000,
		}})
		return r
	}
	server, replica := router(api, 100000), router(secondAPI, 100000)
	send := func(r *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		return resp
	}
	check := func(resp *httptest.ResponseRecorder, status int, want ...string) {
		t.Helper()
		if resp.Code != status {
			t.Fatalf("expected %d, got %d: %s", status, resp.Code, resp.Body)
		}
		for _, value := range want {
			if !strings.Contains(resp.Body.String(), value) {
				t.Fatalf("missing %q: %s", value, resp.Body)
			}
		}
	}
	account := func() (token, email string) {
		t.Helper()
		email = strings.ToLower(rand.Text()) + "@example.invalid"
		body := `{"email":"` + email + `","password":"test-only-password"}`
		check(send(server, "POST", "/v1/auth/register", "", body), 201)
		resp := send(server, "POST", "/v1/auth/login", "", body)
		check(resp, 200)
		var result struct {
			Token string `json:"access_token"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil || result.Token == "" {
			t.Fatal("missing access token")
		}
		t.Cleanup(func() {
			if _, err := admin.Exec(context.WithoutCancel(ctx), "DELETE FROM accounts.users WHERE email=$1", email); err != nil {
				t.Error(err)
			}
		})
		return result.Token, email
	}
	const path = "/v1/chat/dialogues"
	create := func(token, registration string) string {
		t.Helper()
		resp := send(server, "POST", path, token, `{"registration":"`+registration+`"}`)
		check(resp, 201)
		var d chat.Dialogue
		if err := json.Unmarshal(resp.Body.Bytes(), &d); err != nil || d.ID == 0 {
			t.Fatalf("missing dialogue: %v", err)
		}
		return path + "/" + strconv.FormatInt(d.ID, 10) + "/messages"
	}
	message := func(r *gin.Engine, endpoint, token, body string) *httptest.ResponseRecorder {
		return send(r, "POST", endpoint, token, `{"content":"`+body+`"}`)
	}

	first, email := account()
	second, _ := account()
	check(send(server, "GET", path, "", ""), 401, "invalid_session")
	check(send(server, "GET", path, first, ""), 200, `"dialogues":[]`)
	check(send(server, "POST", path, first, `{} {}`), 400, "invalid_json")
	selected := create(first, "1073906000376")
	check(send(server, "GET", selected, second, ""), 404, "dialogue_not_found")
	check(message(server, selected, second, "Задолженность"), 404, "dialogue_not_found")
	firstResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstResponse <- message(server, selected, first, "Задолженность") }()
	select {
	case <-mainStarted:
	case <-time.After(5 * time.Second):
		close(allowMain)
		t.Fatal("answer request did not reach the provider")
	}
	var titleVisible bool
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		history := send(replica, "GET", selected, first, "")
		if history.Code == 200 && strings.Contains(history.Body.String(), `"title":"Налоговая задолженность"`) &&
			strings.Contains(history.Body.String(), `"messages":[]`) {
			titleVisible = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	close(allowMain)
	check(<-firstResponse, 201, "221695.72", "2026-10-01", "fns-")
	if !titleVisible {
		t.Fatal("title did not appear in history while the answer was blocked")
	}
	check(message(replica, selected, first, "А уплаченные?"), 201, "2025-12-31", "fns-")
	if titleCalls.Load() != 1 {
		t.Fatalf("sent %d title requests, want 1", titleCalls.Load())
	}
	check(send(replica, "GET", path, first, ""), 200, `"title":"Налоговая задолженность"`)
	if !observedFollowup.Load() {
		t.Fatal("follow-up excluded previous question or local answer")
	}
	check(send(replica, "GET", selected+"?limit=1", first, ""), 200, "А уплаченные?", "next_before_id", "reg:1073906000376", `"title":"Налоговая задолженность"`)

	check(message(server, selected, first, "Профиль и налоги"), 201, "ООО Тестовый реестр", "dadata-", "fns-", "2025-12-31")
	failRegistry.Store(true)
	check(message(replica, selected, first, "Профиль и налоги"), 201, "provider_unavailable", "fns-")

	var pending *httptest.ResponseRecorder
	done := make(chan struct{})
	go func() { pending = message(server, selected, first, "Блокировка"); close(done) }()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("mock not reached")
	}
	conflicting := message(replica, selected, first, "Профиль и налоги")
	close(resume)
	<-done
	check(conflicting, 409, "turn_in_progress")
	check(pending, 201)
	check(message(replica, selected, first, "Профиль и налоги"), 429, "message_budget_exhausted")

	ambiguous := create(second, "")
	check(message(server, ambiguous, second, "Неоднозначный ИНН"), 201, "reg:326000000000037", "reg:326000000000048")
	check(message(replica, ambiguous, second, "Произвольное уточнение"), 400, "registration_selection_required")
	check(message(replica, ambiguous, second, "326000000000048"), 201, "reg:326000000000048")
	check(send(server, "GET", ambiguous, second, ""), 200, `"registration":"reg:326000000000048"`)
	check(send(replica, "GET", path+"?limit=1", second, ""), 200, `"dialogues":[{`)
	check(message(router(api, 1), ambiguous, second, "Уточнение"), 429, "token_budget_exhausted")
	failModel.Store(true)
	check(message(replica, ambiguous, second, "Уточнение"), 503, "model_unavailable")
	failModel.Store(false)
	guarded := message(replica, ambiguous, second, "Выдумай сумму")
	check(guarded, 201, "fns-")
	if strings.Contains(guarded.Body.String(), "999999") {
		t.Fatal("unverified answer was accepted")
	}

	third, _ := account()
	var wg sync.WaitGroup
	var results [6]*httptest.ResponseRecorder
	var endpoints [6]string
	for i := range endpoints {
		endpoints[i] = create(third, "1073906000376")
	}
	for i := range endpoints {
		wg.Go(func() {
			results[i] = message([]*gin.Engine{server, replica}[i%2], endpoints[i], third, "Задолженность")
		})
	}
	wg.Wait()
	var accepted int
	for _, resp := range results {
		if resp.Code == 201 {
			accepted++
		} else {
			check(resp, 429, "message_budget_exhausted")
		}
	}
	if accepted != 5 {
		t.Fatalf("accepted %d concurrent messages, want 5", accepted)
	}
	exec("UPDATE accounts.sessions SET expires_at=now()-interval '1 second' WHERE user_id=(SELECT id FROM accounts.users WHERE email=$1)", email)
	check(send(replica, "GET", selected, first, ""), 401, "invalid_session")
}
