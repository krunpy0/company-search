package dadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLookup(t *testing.T) {
	for _, tc := range []struct {
		name, mode, path, query, kind, typ string
		count, status                      int
		body, warning                      string
		results                            int
	}{
		{"exact", "inn", "findById", "3907056353", "legal", "LEGAL", 300, 200,
			`{"suggestions":[{"value":"Example","data":{"type":"LEGAL","branch_type":"MAIN","inn":"3907056353","ogrn":"1073906000376"}}]}`, "", 1},
		{"suggest", "name", "suggest", "Пример", "entrepreneur", "INDIVIDUAL", 20, 200, `{"suggestions":[]}`, "", 0},
		{"forbidden", "inn", "findById", "3907056353", "", "", 300, 403, "", "provider_forbidden", 0},
		{"rate limited", "inn", "findById", "3907056353", "", "", 300, 429, "", "provider_rate_limited", 0},
		{"server error", "inn", "findById", "3907056353", "", "", 300, 503, "", "provider_unavailable", 0},
		{"timeout", "inn", "findById", "3907056353", "", "", 300, 0, "", "provider_timeout", 0},
		{"malformed", "inn", "findById", "3907056353", "", "", 300, 200, `{}`, "provider_invalid_response", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/suggestions/api/4_1/rs/"+tc.path+"/party" ||
					r.Header.Get("Authorization") != "Token fixture" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("unexpected provider request")
				}
				var request struct {
					Query, Type string
					BranchType  string `json:"branch_type"`
					Count       int
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Query != tc.query ||
					request.Type != tc.typ || request.Count != tc.count || (tc.mode == "inn" && request.BranchType != "MAIN") {
					t.Errorf("unexpected request body: %+v, %v", request, err)
				}
				if tc.status == 0 {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client, err := New("fixture", 10)
			if err != nil {
				t.Fatal(err)
			}
			client.baseURL = server.URL
			if tc.status == 0 {
				client.http.Timeout = 200 * time.Millisecond
			}
			items, limited, warning := client.Lookup(context.Background(), tc.query, tc.mode, tc.kind)
			if len(items) != tc.results || limited || warning != tc.warning {
				t.Fatalf("unexpected result: %+v, limited=%v, warning=%q", items, limited, warning)
			}
			if len(items) == 1 && (items[0].BranchType != "MAIN" || items[0].DisplayName != "Example") {
				t.Fatalf("unexpected decoded party: %+v", items[0])
			}
			if tc.status == 429 && client.reserve(time.Now().Add(2*time.Second)) != "provider_rate_limited" {
				t.Fatal("Retry-After did not extend the cooldown")
			}
		})
	}
}

func TestRequestBudget(t *testing.T) {
	client, err := New("fixture", 2)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 20, 59, 50, 0, time.UTC)
	for _, step := range []struct {
		seconds time.Duration
		want    string
	}{
		{0, ""}, {0, "provider_rate_limited"}, {1, ""}, {2, "provider_budget_exhausted"}, {10, ""},
	} {
		if got := client.reserve(now.Add(step.seconds * time.Second)); got != step.want {
			t.Fatalf("after %ds got %q, want %q", step.seconds, got, step.want)
		}
	}
}
