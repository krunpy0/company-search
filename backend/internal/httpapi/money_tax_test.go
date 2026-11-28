package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPublishedMoneyTaxes(t *testing.T) {
	dsn := os.Getenv("COMPANY_API_STAGE7_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires the official payment, debt and legal regime releases")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	router := gin.New()
	RegisterCompaniesWithLimiters(router, pool, nil, false, nil)

	get := func(path string, want int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/v1/companies"+path, nil))
		if w.Code != want {
			t.Fatalf("%s: got %d, want %d: %s", path, w.Code, want, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	regimes := get("/inn:0522014502/taxes", 200)
	if regimes["state"] != "found" || regimes["items"].([]any)[0].(map[string]any)["codes"].([]any)[0] != "usn" {
		t.Fatalf("unexpected default tax section: %v", regimes)
	}
	const debtPath = "/inn:6612025806/taxes?section=debts"
	debts := get(debtPath+"&as_of=2026-10-01&limit=1", 200)
	item := debts["items"].([]any)[0].(map[string]any)
	amounts := item["debt"].(map[string]any)
	if debts["state"] != "found" || debts["next_offset"] != float64(1) ||
		item["document_id"] != "d96b0983-4a38-4522-b149-443b9a502cab" || item["as_of"] != "2026-10-01" ||
		amounts["principal"] != "221695.72" || amounts["penalties"] != "0.00" || amounts["total"] != "221695.72" {
		t.Fatalf("unexpected debt page: %v", debts)
	}
	next := get(debtPath+"&limit=1&offset=1", 200)
	if next["items"].([]any)[0].(map[string]any)["tax_name"] != "Страховые взносы" {
		t.Fatalf("unexpected next page: %v", next)
	}
	missing := get(debtPath+"&as_of=2025-10-01", 200)
	if missing["state"] != "not_loaded" || len(missing["items"].([]any)) != 0 {
		t.Fatalf("unexpected unloaded date: %v", missing)
	}
	get(debtPath+"&year=2025", 400)
	search := get("?mode=inn&q=6612025806", 200)
	if len(search["items"].([]any)) != 1 {
		t.Fatalf("expected tax-only exact match: %v", search)
	}
	payments := get("/inn:7838080688/taxes?section=payments&year=2025", 200)
	absent := get("/inn:7838080688/taxes?section=debts", 200)
	if absent["state"] != "not_published" || len(absent["items"].([]any)) != 0 {
		t.Fatalf("unexpected missing debt response: %v", absent)
	}
	source := debts["sources"].(map[string]any)[item["source_ref"].(string)].(map[string]any)
	if source["dataset"] != "debtam" || item["file"] == "" {
		t.Fatalf("unexpected debt source: %v", source)
	}
	var matched bool
	for _, raw := range payments["items"].([]any) {
		row := raw.(map[string]any)
		if row["document_id"] == "620ab455-a239-46c9-9fd7-12175c6ce2f0" && row["tax_name"] == "Налог на добавленную стоимость" {
			matched = row["amount"] == "0.00" && row["as_of"] == "2025-12-31" && row["year"] == float64(2025)
		}
	}
	if !matched {
		t.Fatalf("missing published payment zero: %v", payments)
	}
}
