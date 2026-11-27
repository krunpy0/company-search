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
)

func TestPasswordResetPostgreSQL(t *testing.T) {
	adminURL := os.Getenv("COMPANY_ACCOUNTS_TEST_ADMIN_URL")
	apiURL := os.Getenv("COMPANY_ACCOUNTS_TEST_API_URL")
	if adminURL == "" || apiURL == "" {
		t.Skip("set COMPANY_ACCOUNTS_TEST_ADMIN_URL and COMPANY_ACCOUNTS_TEST_API_URL with migrations and db-roles.sql applied")
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
	admin, api := connect(adminURL), connect(apiURL)
	var database string
	if err := admin.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "stage14_test") {
		t.Fatalf("use a dedicated stage14_test database: %v", err)
	}
	const email = "reset@example.com"
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DELETE FROM accounts.users WHERE email=$1", email); err != nil {
			t.Error(err)
		}
	})
	tokens := make(chan string, 2)
	router := gin.New()
	cfg := DefaultRateLimitConfig()
	cfg.Enabled = false
	RegisterAuthWithLimiters(router, api, NewRateLimiters(cfg), func(_ context.Context, recipient, token string) error {
		if recipient != email {
			t.Errorf("unexpected recipient %q", recipient)
		}
		tokens <- token
		return nil
	})
	send := func(method, path, body, session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/v1/"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if session != "" {
			req.Header.Set("Authorization", "Bearer "+session)
		}
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		return resp
	}
	check := func(resp *httptest.ResponseRecorder, status int) {
		t.Helper()
		if resp.Code != status {
			t.Fatalf("expected status %d, got %d: %s", status, resp.Code, resp.Body)
		}
	}
	readToken := func() string {
		t.Helper()
		select {
		case token := <-tokens:
			return token
		case <-time.After(2 * time.Second):
			t.Fatal("no reset email")
			return ""
		}
	}
	const oldCredentials = `{"email":"reset@example.com","password":"old-test-password"}`
	const newCredentials = `{"email":"reset@example.com","password":"new-test-password"}`
	const requestPath = "auth/password-reset/request"
	const requestBody = `{"email":"reset@example.com"}`
	confirm := func(token string) *httptest.ResponseRecorder {
		return send("POST", "auth/password-reset/confirm", `{"token":"`+token+`","password":"new-test-password"}`, "")
	}
	check(send("POST", "auth/register", oldCredentials, ""), 201)
	login := send("POST", "auth/login", oldCredentials, "")
	check(login, 200)
	var session struct {
		Token string `json:"access_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.Token == "" {
		t.Fatal("missing access token")
	}
	unknown := send("POST", requestPath, `{"email":"unknown@example.com"}`, "")
	known := send("POST", requestPath, requestBody, "")
	check(unknown, 202)
	check(known, 202)
	if unknown.Body.String() != known.Body.String() {
		t.Fatal("response discloses whether the email exists")
	}
	expired := readToken()
	check(send("POST", requestPath, requestBody, ""), 202)
	if _, err := admin.Exec(ctx, "UPDATE accounts.users SET reset_expires_at=now() WHERE email=$1", email); err != nil {
		t.Fatal(err)
	}
	check(confirm(expired), 400)
	check(send("POST", requestPath, requestBody, ""), 202)
	token := readToken()
	if expired == token {
		t.Fatal("reset token was reused")
	}
	var first, second *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Go(func() { first = confirm(token) })
	wg.Go(func() { second = confirm(token) })
	wg.Wait()
	if first.Code == 400 {
		first, second = second, first
	}
	check(first, 204)
	check(second, 400)
	check(send("GET", "account/profile", "", session.Token), 401)
	check(send("POST", "auth/login", oldCredentials, ""), 401)
	check(send("POST", "auth/login", newCredentials, ""), 200)
}
