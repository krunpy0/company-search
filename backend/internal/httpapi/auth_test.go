package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/quota"
)

// Run against a dedicated database with migrations and db-roles.sql applied.
func TestAccountSessionsAndExternalQuota(t *testing.T) {
	adminDSN := os.Getenv("COMPANY_ACCOUNTS_TEST_ADMIN_URL")
	apiDSN := os.Getenv("COMPANY_ACCOUNTS_TEST_API_URL")
	if adminDSN == "" || apiDSN == "" {
		t.Skip("set COMPANY_ACCOUNTS_TEST_ADMIN_URL and COMPANY_ACCOUNTS_TEST_API_URL")
	}
	ctx := t.Context()
	connect := func(dsn string) *pgxpool.Pool {
		t.Helper()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	admin, api, replica := connect(adminDSN), connect(apiDSN), connect(apiDSN)
	var database string
	if err := admin.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "stage14_test") {
		t.Fatalf("use a dedicated stage14_test database: %v", err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("TRUNCATE accounts.users, accounts.sessions, accounts.external_usage, accounts.external_credentials, accounts.chat_dialogues, accounts.chat_turns, accounts.chat_message_usage, accounts.model_token_usage")

	newRouter := func(pool, quotaPool *pgxpool.Pool, providerLimit int) *gin.Engine {
		router := gin.New()
		session := RegisterAuthWithLimiters(router, pool, nil, nil)
		gate := quota.New(quotaPool, "dadata", "shared-test-key", providerLimit, 1)
		RegisterCompaniesWithLimiters(router.Group("", session), pool, func(ctx context.Context, _, _, _ string) ([]company.RegistryParty, bool, string) {
			return nil, false, gate.Reserve(ctx)
		}, false, nil)
		return router
	}
	router := newRouter(api, replica, 2)
	send := func(server *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		result := httptest.NewRecorder()
		server.ServeHTTP(result, req)
		return result
	}
	check := func(result *httptest.ResponseRecorder, status int, contains string) {
		t.Helper()
		if result.Code != status || !strings.Contains(result.Body.String(), contains) {
			t.Fatalf("wanted status %d with %q, got %d: %s", status, contains, result.Code, result.Body)
		}
	}
	register := func(email string) string {
		t.Helper()
		body := `{"email":"` + email + `","password":"long-test-password"}`
		check(send(router, "POST", "/v1/auth/register", "", body), 201, "user_id")
		return body
	}
	login := func(body string) string {
		t.Helper()
		resp := send(router, "POST", "/v1/auth/login", "", body)
		check(resp, 200, "expires_at")
		var session struct {
			Token string `json:"access_token"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &session); err != nil || session.Token == "" {
			t.Fatal("missing session token")
		}
		return session.Token
	}
	firstBody, secondBody := register("first@example.com"), register("second@example.com")
	check(send(router, "POST", "/v1/auth/register", "", firstBody), 409, "email_in_use")
	check(send(router, "POST", "/v1/auth/login", "", strings.Replace(firstBody, "long-test-password", "wrong-test-password", 1)), 401, "invalid_credentials")
	first, second, otherFirst := login(firstBody), login(secondBody), login(firstBody)
	const search = "/v1/companies?q=Example"
	check(send(router, "GET", search, "", ""), 401, "invalid_session")

	otherRouter := newRouter(replica, api, 2)
	var responses [2]*httptest.ResponseRecorder
	var wg sync.WaitGroup
	for i, server := range []*gin.Engine{router, otherRouter} {
		wg.Go(func() { responses[i] = send(server, "GET", search, first, "") })
	}
	wg.Wait()
	throttled := 0
	for _, response := range responses {
		check(response, 200, "")
		if strings.Contains(response.Body.String(), "provider_rate_limited") {
			throttled++
		}
	}
	if throttled != 1 {
		t.Fatal("expected exactly one of two concurrent calls to be throttled")
	}
	resetInterval := func() { exec("UPDATE accounts.external_credentials SET next_at='-infinity'") }
	resetInterval()
	check(send(otherRouter, "GET", search, otherFirst, ""), 200, "user_external_api_budget_exhausted")
	check(send(otherRouter, "GET", search, second, ""), 200, "")

	restarted := newRouter(connect(apiDSN), connect(apiDSN), 2)
	check(send(restarted, "GET", search, second, ""), 200, "provider_budget_exhausted")
	var providerUsed, userUsed int
	if err := admin.QueryRow(ctx, "SELECT used, (SELECT sum(used) FROM accounts.external_usage) FROM accounts.external_credentials").Scan(&providerUsed, &userUsed); err != nil || providerUsed != 2 || userUsed != 2 {
		t.Fatalf("wanted two provider and user reservations, got %d/%d: %v", providerUsed, userUsed, err)
	}
	exec("UPDATE accounts.external_credentials SET day=day-1, next_at='-infinity'")
	exec("UPDATE accounts.external_usage SET day=day-1")
	gate := quota.New(api, "dadata", "shared-test-key", 2, 1)
	if err := gate.Backoff(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(send(restarted, "GET", search, second, ""), 200, "provider_rate_limited")
	exec("UPDATE accounts.external_credentials SET cooldown_until='-infinity'")
	check(send(restarted, "GET", search, second, ""), 200, `"warnings":[]`)

	check(send(restarted, "POST", "/v1/auth/logout", first, ""), 204, "")
	check(send(router, "GET", search, first, ""), 401, "invalid_session")
	check(send(router, "GET", search, otherFirst, ""), 200, "")
	exec("UPDATE accounts.sessions SET expires_at=now()-interval '1 second'")
	check(send(router, "GET", search, second, ""), 401, "invalid_session")
	active := login(firstBody)
	for _, remaining := range []int{2, 1, 1} {
		if err := deleteExpiredSessions(ctx, api, 1); err != nil {
			t.Fatal(err)
		}
		var sessions int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM accounts.sessions").Scan(&sessions); err != nil || sessions != remaining {
			t.Fatalf("wanted %d sessions after cleanup, got %d: %v", remaining, sessions, err)
		}
	}
	check(send(router, "GET", search, active, ""), 200, "")

	var isolated bool
	if err := admin.QueryRow(ctx, `
		SELECT NOT has_schema_privilege('company_importer', 'accounts', 'USAGE')
		   AND NOT has_table_privilege('company_importer', 'accounts.users', 'SELECT')
		   AND has_table_privilege('company_importer', 'public.import_runs', 'INSERT')
		   AND NOT has_table_privilege('company_api', 'public.import_runs', 'INSERT')
	`).Scan(&isolated); err != nil || !isolated {
		t.Fatalf("database roles are not isolated: %v", err)
	}
}
