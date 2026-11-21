package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/dadata"
)

func TestInvalidArgumentsBeforeRead(t *testing.T) {
	for name := range operations {
		if _, ok := schemas[name]; !ok {
			t.Fatalf("missing schema for %s", name)
		}
	}
	if len(operations) != len(schemas) {
		t.Fatal("schema without an executor")
	}

	for _, tt := range []struct{ name, args, code string }{
		{"unknown", "{}", "unknown_tool"},
		{"search_companies", "{}", "invalid_arguments"},
		{"search_companies", `{"q":"Гефест","user_id":"model"}`, "invalid_arguments"},
		{"search_companies", `{"q":"Гефест","q":"Дубль"}`, "invalid_arguments"},
		{"search_companies", `{"q":null}`, "invalid_arguments"},
		{"search_companies", `{"q":"Гефест","limit":"1"}`, "invalid_arguments"},
		{"search_companies", `{"q":"Гефест","limit":101}`, "invalid_arguments"},
		{"search_companies", `{"q":"Гефест","mode":"unknown"}`, "invalid_arguments"},
		{"search_companies", `{"q":"Гефест"} []`, "invalid_arguments"},
		{"search_companies", `{"q":"3907056354"}`, "invalid_query"},
		{"search_companies", `{"q":"Гефест","cursor":"?"}`, "invalid_cursor"},
		{"get_company_profile", `{"id":"reg:1073906000377"}`, "invalid_id"},
		{"get_company_taxes", `{"id":"inn:3907056353","as_of":"2026-02-30"}`, "invalid_arguments"},
		{"get_company_taxes", `{"id":"inn:3907056353","section":"debts","year":2025}`, "invalid_query"},
		{"get_company_taxes", `{"id":"inn:3907056353","offset":0}`, "invalid_query"},
	} {
		t.Run(tt.name+"/"+tt.args, func(t *testing.T) {
			// A nil pool makes any premature database call fail the test.
			result := (Executor{}).Execute(context.Background(), "", tt.name, json.RawMessage(tt.args))
			if result.Error == nil || result.Error.Code != tt.code {
				t.Fatalf("got %+v, want %s", result, tt.code)
			}
		})
	}
}

func TestExecutePostgreSQL(t *testing.T) {
	dsn := os.Getenv("COMPANY_TOOLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated PostgreSQL with testdata/stage10.sql")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	e := Executor{Reader: company.Reader{Pool: pool}}
	call := func(name string, args string) map[string]any {
		t.Helper()
		result := e.Execute(ctx, "", name, json.RawMessage(args))
		if result.Error != nil {
			t.Fatalf("%s %s: %v", name, args, result.Error)
		}
		data, err := json.Marshal(result.Data)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	checkEvidence := func(body map[string]any, observation map[string]any, dataset, asOf string) {
		t.Helper()
		ref, ok := observation["source_ref"].(string)
		source, resolved := body["sources"].(map[string]any)[ref].(map[string]any)
		if !ok || !resolved || source["dataset"] != dataset || observation["as_of"] != asOf {
			t.Fatalf("unexpected evidence: %v -> %v", observation, source)
		}
	}

	page := call("search_companies", `{"q":"500100732259","limit":1}`)
	first := page["items"].([]any)[0].(map[string]any)
	checkEvidence(page, first["registration_evidence"].(map[string]any), "rsmp", "2026-11-10")
	nextArgs, _ := json.Marshal(map[string]any{"q": "500100732259", "limit": 1, "cursor": page["next_cursor"]})
	next := call("search_companies", string(nextArgs))
	if next["items"].([]any)[0].(map[string]any)["id"] == first["id"] {
		t.Fatal("repeated first registration on the next page")
	}

	ambiguous := e.Execute(ctx, "", "get_company_profile", json.RawMessage(`{"id":"inn:500100732259"}`))
	if ambiguous.Data != nil || ambiguous.Error == nil || ambiguous.Error.Code != "ambiguous_identity" ||
		len(ambiguous.Error.Candidates) != 2 {
		t.Fatalf("expected two candidates without an arbitrary profile: %+v", ambiguous)
	}
	missing := e.Execute(ctx, "", "get_company_profile", json.RawMessage(`{"id":"inn:0000000000"}`))
	if missing.Data != nil || missing.Error == nil || missing.Error.Code != "company_not_found" {
		t.Fatalf("expected missing identity: %+v", missing)
	}

	finance := call("get_company_financials", `{"id":"reg:1073906000376","year":2025}`)
	item := finance["items"].([]any)[0].(map[string]any)
	checkEvidence(finance, item, "revexp", "2025-12-31")
	if item["income"] != "0.00" {
		t.Fatalf("expected a published zero, got %v", item)
	}
	debts := call("get_company_taxes", `{"id":"inn:6612025806","section":"debts","as_of":"2026-10-01","limit":1}`)
	item = debts["items"].([]any)[0].(map[string]any)
	checkEvidence(debts, item, "debtam", "2026-10-01")
	if debts["next_offset"] != float64(1) || item["debt"].(map[string]any)["total"] != "221695.72" {
		t.Fatalf("unexpected first debt page: %v", debts)
	}
	payments := call("get_company_taxes", `{"id":"inn:7838080688","section":"payments","year":2025}`)
	item = payments["items"].([]any)[0].(map[string]any)
	checkEvidence(payments, item, "paytax", "2025-12-31")
	if item["amount"] != "0.00" {
		t.Fatalf("expected a published payment zero, got %v", item)
	}

	for _, tt := range []struct{ tool, args, state string }{
		{"get_company_financials", `{"id":"inn:6612025806","year":2025}`, "not_published"},
		{"get_company_financials", `{"id":"reg:326000000000037"}`, "not_applicable"},
		{"get_company_taxes", `{"id":"inn:6612025806","section":"debts","as_of":"2025-10-01"}`, "not_loaded"},
	} {
		body := call(tt.tool, tt.args)
		if body["state"] != tt.state || body["reason"] == nil || len(body["items"].([]any)) != 0 || body["source_ref"] != nil {
			t.Fatalf("expected %s without invented evidence: %v", tt.state, body)
		}
	}

	sources := call("get_company_sources", `{"id":"reg:1073906000376"}`)
	observation := sources["observations"].([]any)[0].(map[string]any)
	checkEvidence(sources, observation["evidence"].(map[string]any), "rsmp", "2026-11-10")

	e.Reader.Lookup = func(context.Context, string, string, string) ([]dadata.Party, bool, string) {
		return nil, false, "provider_timeout"
	}
	profile := call("get_company_profile", `{"id":"reg:1073906000376"}`)
	registration := profile["sections"].(map[string]any)["registration"].(map[string]any)
	local := profile["company"].(map[string]any)
	checkEvidence(profile, local["registration_evidence"].(map[string]any), "rsmp", "2026-11-10")
	if registration["state"] != "unavailable" || registration["reason"] == nil ||
		profile["coverage"].(map[string]any)["dadata"] != "unavailable" ||
		profile["warnings"].([]any)[0].(map[string]any)["code"] != "provider_timeout" {
		t.Fatalf("expected local evidence and an explicit partial result: %v", profile)
	}
	e.Reader.Lookup = func(context.Context, string, string, string) ([]dadata.Party, bool, string) {
		return []dadata.Party{{INN: "3907056353", OGRN: "1073906000376", Type: "LEGAL", BranchType: "MAIN"}}, false, ""
	}
	profile = call("get_company_profile", `{"id":"reg:1073906000376"}`)
	external := profile["dadata"].(map[string]any)
	source := profile["sources"].(map[string]any)[external["source_ref"].(string)].(map[string]any)
	if external["as_of"] != nil || source["publisher"] != "DaData" || source["url"] != external["source_url"] ||
		external["retrieved_at"] == nil {
		t.Fatalf("expected dated retrieval and unknown as_of: %v -> %v", external, source)
	}
	if value, exists := external["as_of"]; !exists || value != nil {
		t.Fatalf("expected explicit as_of: null, got %v", external)
	}
}
