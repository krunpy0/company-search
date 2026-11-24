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
			`{"suggestions":[{"value":"Example","data":{"type":"LEGAL","branch_type":"MAIN","inn":"3907056353","ogrn":"1073906000376","management":{"name":"Example manager","post":"Director","start_date":1669237200000},"address":{"value":"Example address"}}}]}`, "", 1},
		{"registration ip", "registration", "findById", "326000000000015", "entrepreneur", "INDIVIDUAL", 300, 200,
			`{"suggestions":[{"value":"Test IP","data":{"type":"INDIVIDUAL","inn":"000000000000","ogrn":"326000000000015","address":{"value":"Test city","unrestricted_value":"Private address"}}}]}`, "", 1},
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
			var cooldown time.Time
			client := &Client{
				token: "fixture", baseURL: server.URL, http: server.Client(),
				reserve: func(context.Context) string { return "" },
				setCooldown: func(_ context.Context, until time.Time) error {
					cooldown = until
					return nil
				},
			}
			if tc.status == 0 {
				client.http.Timeout = 200 * time.Millisecond
			}
			items, limited, warning := client.Lookup(context.Background(), tc.query, tc.mode, tc.kind)
			if len(items) != tc.results || limited || warning != tc.warning {
				t.Fatalf("unexpected result: %+v, limited=%v, warning=%q", items, limited, warning)
			}
			if tc.name == "exact" && (items[0].Kind != "legal" || items[0].Name != "Example" || items[0].Manager.Name != "Example manager" ||
				items[0].Manager.Title != "Director" || items[0].Manager.StartedOn != "2022-11-24" || items[0].Address != "Example address") {
				t.Fatalf("unexpected decoded party: %+v", items[0])
			}
			if tc.name == "registration ip" && (items[0].Kind != "entrepreneur" ||
				items[0].ResidenceCity != "Test city" || items[0].Address != "") {
				t.Fatalf("unexpected decoded entrepreneur: %+v", items[0])
			}
			if tc.status == 429 && !cooldown.After(time.Now().Add(time.Minute)) {
				t.Fatal("Retry-After did not extend the cooldown")
			}
		})
	}
}
