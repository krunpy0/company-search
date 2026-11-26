package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/agent"
	"github.com/krunpy0/company-search/backend/internal/chat"
)

const (
	profilePath   = "/v1/account/profile"
	avatarPath    = "/v1/account/avatar"
	passwordPath  = "/v1/account/password"
	accountPath   = "/v1/account"
	dialoguesPath = "/v1/chat/dialogues"
	testPassword  = "initial-test-password"
	otherPassword = "invalid-test-password"
	newPassword   = "updated-test-password"
)

func stage16Pools(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	adminDSN := os.Getenv("COMPANY_STAGE16_TEST_ADMIN_URL")
	apiDSN := os.Getenv("COMPANY_STAGE16_TEST_API_URL")
	if adminDSN == "" || apiDSN == "" {
		t.Skip("set COMPANY_STAGE16_TEST_ADMIN_URL and COMPANY_STAGE16_TEST_API_URL")
	}
	connect := func(dsn string) *pgxpool.Pool {
		t.Helper()
		pool, err := pgxpool.New(t.Context(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	admin, api := connect(adminDSN), connect(apiDSN)
	var database, apiDatabase, apiRole string
	if err := admin.QueryRow(t.Context(), "SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "stage16_test") {
		t.Fatalf("use a dedicated stage16_test database: %v", err)
	}
	if err := api.QueryRow(t.Context(), "SELECT current_database(), current_user").Scan(&apiDatabase, &apiRole); err != nil ||
		apiDatabase != database || apiRole != "company_api" {
		t.Fatalf("use company_api on the same dedicated stage16_test database: %v", err)
	}
	if _, err := admin.Exec(t.Context(), "TRUNCATE accounts.users CASCADE"); err != nil {
		t.Fatal(err)
	}
	return admin, api
}

func stage16Send(router *gin.Engine, method, path, token, contentType string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func stage16Call(t *testing.T, router *gin.Engine, method, path, token, body string, status int, contains string) *httptest.ResponseRecorder {
	t.Helper()
	result := stage16Send(router, method, path, token, "application/json", []byte(body))
	stage16Check(t, result, status, contains)
	return result
}

func stage16Check(t *testing.T, response *httptest.ResponseRecorder, status int, contains string) {
	t.Helper()
	if response.Code != status || !strings.Contains(response.Body.String(), contains) {
		t.Fatalf("wanted status %d with %q, got %d: %.1500s", status, contains, response.Code, response.Body)
	}
}

func stage16Decode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var output T
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func stage16Credentials(email, password string) string {
	return `{"email":"` + email + `","password":"` + password + `"}`
}

func stage16Register(t *testing.T, r *gin.Engine, email string) {
	t.Helper()
	stage16Call(t, r, "POST", "/v1/auth/register", "", stage16Credentials(email, testPassword), 201, "user_id")
}

func stage16Login(t *testing.T, r *gin.Engine, email, password string) string {
	t.Helper()
	result := stage16Call(t, r, "POST", "/v1/auth/login", "", stage16Credentials(email, password), 200, "access_token")
	return stage16Decode[struct {
		Token string `json:"access_token"`
	}](t, result).Token
}

func TestStage16AccountHTTP(t *testing.T) {
	admin, api := stage16Pools(t)
	r := gin.New()
	session := RegisterAuth(r, api)
	RegisterChat(r.Group("", session), chat.Service{Store: chat.Store{Pool: api}})
	const firstEmail = "owner@example.test"
	const secondEmail = "another@example.test"
	const nextEmail = "owner-new@example.test"
	stage16Register(t, r, firstEmail)
	stage16Register(t, r, secondEmail)
	first := stage16Login(t, r, firstEmail, testPassword)
	oldSession := stage16Login(t, r, firstEmail, testPassword)
	other := stage16Login(t, r, secondEmail, testPassword)

	call := func(method, path, token, body string, status int, contains string) *httptest.ResponseRecorder {
		t.Helper()
		return stage16Call(t, r, method, path, token, body, status, contains)
	}
	checkProfile := func(token, email, displayName string, avatar bool) accountProfile {
		t.Helper()
		result := call("GET", profilePath, token, "", 200, "")
		actual := stage16Decode[accountProfile](t, result)
		if result.Header().Get("Cache-Control") != "no-store" || actual.UserID <= 0 ||
			actual.Email != email || actual.DisplayName != displayName || actual.HasAvatar != avatar {
			t.Fatalf("unexpected profile or cache headers: %#v, %v", actual, result.Header())
		}
		return actual
	}
	for _, endpoint := range []struct{ method, path string }{
		{"GET", profilePath}, {"PATCH", profilePath}, {"GET", avatarPath}, {"PUT", avatarPath},
		{"DELETE", avatarPath}, {"POST", passwordPath}, {"DELETE", accountPath},
	} {
		call(endpoint.method, endpoint.path, "", "", 401, "invalid_session")
	}
	owner := checkProfile(first, firstEmail, "", false)
	call("PATCH", profilePath, first, `{}`, 400, "invalid_profile")
	call("PATCH", profilePath, first, `{"display_name":"  Анна Иванова  "}`, 200, "Анна Иванова")
	call("PATCH", profilePath, first, `{"display_name":"`+strings.Repeat("Я", 81)+`"}`, 400, "invalid_profile")
	call("PATCH", profilePath, first, `{"email":"not-an-email","current_password":"`+testPassword+`"}`, 400, "invalid_profile")
	call("PATCH", profilePath, first, `{"email":"`+nextEmail+`"}`, 400, "invalid_password")
	call("PATCH", profilePath, first, `{"email":"`+nextEmail+`","current_password":"`+otherPassword+`"}`, 403, "incorrect_password")
	call("PATCH", profilePath, first, `{"email":"`+secondEmail+`","display_name":"Ignored","current_password":"`+testPassword+`"}`, 409, "email_in_use")
	checkProfile(first, firstEmail, "Анна Иванова", false)
	call("PATCH", profilePath, first, `{"email":"  OWNER-NEW@EXAMPLE.TEST ","current_password":"`+testPassword+`"}`, 200, nextEmail)
	checkProfile(first, nextEmail, "Анна Иванова", false)
	call("POST", "/v1/auth/login", "", stage16Credentials(firstEmail, testPassword), 401, "invalid_credentials")
	stage16Login(t, r, nextEmail, testPassword)

	call("GET", avatarPath, first, "", 404, "avatar_not_found")
	var original bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 512))
	img.SetNRGBA(0, 0, color.NRGBA{R: 240, G: 10, A: 255})
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	imageWithExtraData := append(original.Bytes(), []byte("private-untrusted-trailing-data")...)
	stage16Check(t, stage16Send(r, "PUT", avatarPath, first, "image/svg+xml", []byte("<svg/>")), 400, "invalid_avatar")
	stage16Check(t, stage16Send(r, "PUT", avatarPath, first, "image/jpeg", original.Bytes()), 400, "invalid_avatar")
	stage16Check(t, stage16Send(r, "PUT", avatarPath, first, "image/png", make([]byte, maxAvatarBytes+1)), 413, "avatar_too_large")
	stage16Check(t, stage16Send(r, "PUT", avatarPath, first, "image/png", imageWithExtraData), 200, `"has_avatar":true`)
	profile := checkProfile(first, nextEmail, "Анна Иванова", true)
	if profile.UserID != owner.UserID {
		t.Fatal("profile owner changed")
	}
	avatar := call("GET", avatarPath, first, "", 200, "")
	got, err := png.Decode(bytes.NewReader(avatar.Body.Bytes()))
	if err != nil || got.Bounds().Dx() != 512 || got.Bounds().Dy() != 256 ||
		avatar.Header().Get("Content-Type") != "image/png" || avatar.Header().Get("Cache-Control") != "no-store" ||
		avatar.Header().Get("X-Content-Type-Options") != "nosniff" ||
		bytes.Contains(avatar.Body.Bytes(), []byte("private-untrusted-trailing-data")) {
		t.Fatalf("avatar was not safely normalized: %v, headers %v", err, avatar.Header())
	}
	checkProfile(other, secondEmail, "", false)
	call("GET", avatarPath, other, "", 404, "avatar_not_found")
	call("DELETE", avatarPath, other, "", 204, "")
	checkProfile(first, nextEmail, "Анна Иванова", true)
	var jpegAvatar bytes.Buffer
	if err := jpeg.Encode(&jpegAvatar, img, nil); err != nil {
		t.Fatal(err)
	}
	stage16Check(t, stage16Send(r, "PUT", avatarPath, first, "image/jpeg", jpegAvatar.Bytes()), 200, `"has_avatar":true`)
	call("GET", avatarPath, first, "", 200, "")
	call("DELETE", avatarPath, first, "", 204, "")
	checkProfile(first, nextEmail, "Анна Иванова", false)
	call("GET", avatarPath, first, "", 404, "avatar_not_found")

	call("POST", passwordPath, first, `{"current_password":"`+testPassword+`","new_password":"short"}`, 400, "invalid_password")
	call("POST", passwordPath, first, `{"current_password":"`+otherPassword+`","new_password":"`+newPassword+`"}`, 403, "incorrect_password")
	type passwordResult struct {
		token    string
		response *httptest.ResponseRecorder
	}
	passwordUpdates := make(chan passwordResult, 2)
	concurrentLogin := make(chan *httptest.ResponseRecorder, 1)
	ready := make(chan struct{})
	for _, token := range []string{first, oldSession} {
		go func() {
			<-ready
			response := stage16Send(r, "POST", passwordPath, token, "application/json",
				[]byte(`{"current_password":"`+testPassword+`","new_password":"`+newPassword+`"}`))
			passwordUpdates <- passwordResult{token: token, response: response}
		}()
	}
	go func() {
		<-ready
		concurrentLogin <- stage16Send(r, "POST", "/v1/auth/login", "", "application/json",
			[]byte(stage16Credentials(nextEmail, testPassword)))
	}()
	close(ready)
	var winner string
	for range 2 {
		result := <-passwordUpdates
		if result.response.Code == 204 {
			if winner != "" {
				t.Fatal("both password changes succeeded")
			}
			winner = result.token
		} else {
			stage16Check(t, result.response, 401, "invalid_session")
			oldSession = result.token
		}
	}
	if winner == "" {
		t.Fatal("neither password change succeeded")
	}
	first = winner
	loginResult := <-concurrentLogin
	if loginResult.Code == 200 {
		concurrentToken := stage16Decode[struct {
			Token string `json:"access_token"`
		}](t, loginResult).Token
		call("GET", profilePath, concurrentToken, "", 401, "invalid_session")
	} else {
		stage16Check(t, loginResult, 401, "invalid_credentials")
	}
	call("GET", profilePath, oldSession, "", 401, "invalid_session")
	checkProfile(first, nextEmail, "Анна Иванова", false)
	call("POST", "/v1/auth/login", "", stage16Credentials(nextEmail, testPassword), 401, "invalid_credentials")
	secondSession := stage16Login(t, r, nextEmail, newPassword)

	dialogue := stage16Decode[chat.Dialogue](t, call("POST", dialoguesPath, first, `{}`, 201, "id"))
	otherDialogue := stage16Decode[chat.Dialogue](t, call("POST", dialoguesPath, other, `{}`, 201, "id"))
	if _, err := admin.Exec(t.Context(), `INSERT INTO accounts.chat_turns (dialogue_id, question, answer)
		VALUES ($1, 'Owner question', '{"summary":"Owner answer"}')`, dialogue.ID); err != nil {
		t.Fatal(err)
	}
	call("DELETE", accountPath, first, `{"current_password":"`+testPassword+`"}`, 403, "incorrect_password")
	call("GET", profilePath, first, "", 200, nextEmail)
	call("DELETE", accountPath, first, `{"current_password":"`+newPassword+`"}`, 204, "")
	call("GET", profilePath, first, "", 401, "invalid_session")
	call("GET", profilePath, secondSession, "", 401, "invalid_session")
	call("POST", "/v1/auth/login", "", stage16Credentials(nextEmail, newPassword), 401, "invalid_credentials")
	call("GET", dialoguesPath+"/"+strconv.FormatInt(otherDialogue.ID, 10)+"/messages", other, "", 200, "messages")
	var removed bool
	if err := admin.QueryRow(t.Context(), `SELECT NOT EXISTS (SELECT FROM accounts.users WHERE id=$1)
		AND NOT EXISTS (SELECT FROM accounts.sessions WHERE user_id=$1)
		AND NOT EXISTS (SELECT FROM accounts.chat_dialogues WHERE user_id=$1)
		AND NOT EXISTS (SELECT FROM accounts.chat_turns WHERE dialogue_id=$2)`, owner.UserID, dialogue.ID).Scan(&removed); err != nil || !removed {
		t.Fatalf("account, sessions or history survived deletion: %v", err)
	}
}

type stage16Provider func(context.Context) (agent.Message, error)

func (p stage16Provider) Complete(ctx context.Context, _ []agent.Message, _ json.RawMessage) (agent.Message, error) {
	return p(ctx)
}

func TestStage16ChatManagementHTTP(t *testing.T) {
	admin, api := stage16Pools(t)
	r := gin.New()
	started, resume := make(chan struct{}), make(chan struct{})
	session := RegisterAuth(r, api)
	RegisterChat(r.Group("", session), chat.Service{
		Store: chat.Store{Pool: api},
		Run: agent.Run{Steps: 1, Bytes: 10000, Provider: stage16Provider(func(ctx context.Context) (agent.Message, error) {
			close(started)
			select {
			case <-ctx.Done():
				return agent.Message{}, ctx.Err()
			case <-resume:
				return agent.Message{Role: "assistant", Content: "Готово"}, nil
			}
		})},
	})
	const firstEmail = "chat-owner@example.test"
	const otherEmail = "other-chat-owner@example.test"
	stage16Register(t, r, firstEmail)
	stage16Register(t, r, otherEmail)
	first := stage16Login(t, r, firstEmail, testPassword)
	other := stage16Login(t, r, otherEmail, testPassword)

	call := func(method, path, token, body string, status int, contains string) *httptest.ResponseRecorder {
		t.Helper()
		return stage16Call(t, r, method, path, token, body, status, contains)
	}
	dialoguePath := func(id int64) string { return dialoguesPath + "/" + strconv.FormatInt(id, 10) }
	create := func(token, registration string) chat.Dialogue {
		t.Helper()
		return stage16Decode[chat.Dialogue](t, call("POST", dialoguesPath, token, `{"registration":"`+registration+`"}`, 201, "id"))
	}
	firstDialogue := create(first, "1073906000376")
	secondDialogue := create(first, "")
	otherDialogue := create(other, "1073906000376")
	firstPath, secondPath, otherPath := dialoguePath(firstDialogue.ID), dialoguePath(secondDialogue.ID), dialoguePath(otherDialogue.ID)
	call("GET", dialoguesPath+"/search?q=tax", "", "", 401, "invalid_session")
	call("PATCH", firstPath, "", `{"title":"Hi"}`, 401, "invalid_session")
	call("DELETE", firstPath, "", "", 401, "invalid_session")
	call("DELETE", dialoguesPath, "", "", 401, "invalid_session")
	call("PATCH", otherPath, first, `{"title":"Private"}`, 404, "dialogue_not_found")
	call("DELETE", otherPath, first, "", 404, "dialogue_not_found")
	call("PATCH", firstPath, first, `{"title":" "}`, 400, "invalid_title")
	call("PATCH", firstPath, first, `{"title":"`+strings.Repeat("Я", 81)+`"}`, 400, "invalid_title")
	call("PATCH", firstPath, first, `{"title":"  Налоги за 2025  "}`, 200, `"title":"Налоги за 2025"`)
	call("PATCH", otherPath, other, `{"title":"Налоги другого аккаунта"}`, 200, "Налоги")
	call("GET", firstPath+"/messages", first, "", 200, "Налоги за 2025")
	if _, err := admin.Exec(t.Context(), `INSERT INTO accounts.chat_turns (dialogue_id, question, answer)
		VALUES ($1, 'Исторический старый вопрос', '{"summary":"Справка: подтверждены НАЛОГИ","findings":[{"internal":"hidden_metadata"}]}'),
			   ($2, 'Чужой вопрос', '{"summary":"Секретная справка"}')`, secondDialogue.ID, otherDialogue.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), `UPDATE accounts.chat_dialogues SET pending_question='Тестовый вопрос',
		pending_registrations=ARRAY['reg:326000000000048'] WHERE id=$1`, secondDialogue.ID); err != nil {
		t.Fatal(err)
	}
	search := func(query string, cursor string, expected ...int64) string {
		t.Helper()
		path := dialoguesPath + "/search?q=" + url.QueryEscape(query) + "&limit=1" + cursor
		page := stage16Decode[struct {
			Dialogues []chat.Dialogue `json:"dialogues"`
			Cursor    string          `json:"next_before_id"`
		}](t, call("GET", path, first, "", 200, `"dialogues":[`))
		if len(page.Dialogues) != len(expected) {
			t.Fatalf("search %q wanted %v, got %#v", query, expected, page)
		}
		for i, id := range expected {
			if page.Dialogues[i].ID != id {
				t.Fatalf("search %q wanted %v, got %#v", query, expected, page)
			}
		}
		return page.Cursor
	}
	cursor := search("нАлОгИ", "", secondDialogue.ID)
	if cursor == "" || search("НАЛОГИ", "&before_id="+url.QueryEscape(cursor), firstDialogue.ID) != "" {
		t.Fatal("incorrect search cursor")
	}
	search("reg:1073906000376", "", firstDialogue.ID)
	search("326000000000048", "", secondDialogue.ID)
	search("СТАРЫЙ ВОПРОС", "", secondDialogue.ID)
	search("hidden_metadata", "")
	search("Секретная справка", "")
	search("%", "")
	call("GET", dialoguesPath+"/search?q=+", first, "", 400, "invalid_search")
	call("GET", dialoguesPath+"/search?q=test&limit=51", first, "", 400, "invalid_pagination")

	call("DELETE", secondPath, first, "", 204, "")
	call("DELETE", secondPath, first, "", 404, "dialogue_not_found")
	call("GET", secondPath+"/messages", first, "", 404, "dialogue_not_found")
	search("старый вопрос", "")
	var noTurns bool
	if err := admin.QueryRow(t.Context(), `SELECT NOT EXISTS (SELECT FROM accounts.chat_turns WHERE dialogue_id=$1)`, secondDialogue.ID).
		Scan(&noTurns); err != nil || !noTurns {
		t.Fatalf("turns survived dialogue deletion: %v", err)
	}

	inFlight := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		inFlight <- stage16Send(r, "POST", firstPath+"/messages", first, "application/json", []byte(`{"content":"Вопрос в работе"}`))
	}()
	t.Cleanup(func() { close(resume) })
	select {
	case <-started:
	case response := <-inFlight:
		t.Fatalf("send returned before model started: %d %s", response.Code, response.Body)
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	call("DELETE", dialoguesPath, first, "", 204, "")
	call("DELETE", dialoguesPath, first, "", 204, "")
	select {
	case resume <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not finish waiting")
	}
	select {
	case response := <-inFlight:
		stage16Check(t, response, 409, "turn_in_progress")
	case <-time.After(5 * time.Second):
		t.Fatal("send did not finish")
	}
	call("GET", dialoguesPath, first, "", 200, `"dialogues":[]`)
	call("GET", otherPath+"/messages", other, "", 200, "Чужой вопрос")
	var retained int
	if err := admin.QueryRow(t.Context(), `SELECT COALESCE(sum(used),0) FROM accounts.chat_message_usage`).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("message quota changed after history was cleared: got %d, %v", retained, err)
	}
}
