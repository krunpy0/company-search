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

func TestPublishedEmploymentAndRegimes(t *testing.T) {
	dsn := os.Getenv("COMPANY_API_STAGE6_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires the documented October 2026 employment and regime releases and November SME release")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	router := gin.New()
	RegisterCompanies(router, pool, nil, false)
	get := func(path string, status int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/v1/companies"+path, nil))
		if w.Code != status {
			t.Fatalf("%s: got %d, want %d: %s", path, w.Code, status, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	evidence := func(body, item map[string]any, dataset, document, asOf string) {
		t.Helper()
		source := body["sources"].(map[string]any)[item["source_ref"].(string)].(map[string]any)
		if source["dataset"] != dataset || item["as_of"] != asOf || item["document_id"] != document ||
			item["document_date"] != "2026-10-25" || item["file"] == "" {
			t.Fatalf("unexpected evidence: item=%v source=%v", item, source)
		}
	}

	profile := get("/inn:7327032797", 200)
	headcounts := profile["headcounts"].([]any)
	var gotZero bool
	for _, raw := range headcounts {
		item := raw.(map[string]any)
		if item["scope"] == "taxpayer" && item["year"] == float64(2025) && item["employees"] == float64(0) {
			evidence(profile, item, "sshr2019", "a3b9755c-2394-4e5d-8d8b-b4b720648d02", "2025-12-31")
			gotZero = true
		}
	}
	if !gotZero {
		t.Fatalf("expected the published zero, got %v", headcounts)
	}
	profile = get("/inn:6230092529", 200)
	headcounts = profile["headcounts"].([]any)
	if len(headcounts) < 2 {
		t.Fatalf("expected independent SME and headcount observations, got %v", headcounts)
	}
	sme, dedicated := headcounts[0].(map[string]any), headcounts[1].(map[string]any)
	if sme["scope"] != "registration" || sme["year"] != float64(2025) || sme["as_of"] != "2026-11-10" ||
		dedicated["scope"] != "taxpayer" || dedicated["year"] != float64(2025) || dedicated["as_of"] != "2025-12-31" {
		t.Fatalf("expected same report year with distinct observation dates, got %v", headcounts)
	}

	legalTaxes := get("/inn:0522014502/taxes", 200)
	legalItems := legalTaxes["items"].([]any)
	if legalTaxes["state"] != "found" || len(legalItems) != 1 {
		t.Fatalf("expected one legal regime observation, got %v", legalTaxes)
	}
	legal := legalItems[0].(map[string]any)
	legalCodes := legal["codes"].([]any)
	if legal["scope"] != "taxpayer" || len(legalCodes) != 1 || legalCodes[0] != "usn" {
		t.Fatalf("unexpected legal regime codes: %v", legal)
	}
	evidence(legalTaxes, legal, "snr", "2b6870dc-5c2b-4d27-b5f2-c29e81ef92ac", "2026-10-01")
	noRegime := get("/inn:3907056353/taxes", 200)
	if noRegime["state"] != "not_published" || noRegime["as_of"] != "2026-10-01" || len(noRegime["items"].([]any)) != 0 {
		t.Fatalf("a missing document must not imply that no regime applies: %v", noRegime)
	}

	ip := "/reg:325366800093867"
	ipProfile := get(ip, 200)
	headcount := ipProfile["sections"].(map[string]any)["headcount"].(map[string]any)
	if headcount["state"] != "not_applicable" || len(ipProfile["headcounts"].([]any)) != 0 {
		t.Fatal("expected no dedicated employment figure for the entrepreneur")
	}
	taxes := get(ip+"/taxes?section=regimes&as_of=2026-10-01", 200)
	observations := taxes["items"].([]any)
	if taxes["state"] != "found" || len(observations) != 1 {
		t.Fatalf("expected one regime observation, got %v", taxes)
	}
	observation := observations[0].(map[string]any)
	codes := observation["codes"].([]any)
	if observation["scope"] != "registration" || len(codes) != 2 || codes[0] != "usn" || codes[1] != "psn" {
		t.Fatalf("unexpected codes: %v", observation)
	}
	evidence(taxes, observation, "snrip", "b510ed8a-7199-492d-9b3b-b8d6887e9a28", "2026-10-01")
	missing := get(ip+"/taxes?as_of=1900-01-01", 200)
	if missing["state"] != "not_loaded" || len(missing["items"].([]any)) != 0 {
		t.Fatalf("an unloaded snapshot must not return current codes: %v", missing)
	}
	get(ip+"/taxes?as_of=2026-02-30", 400)
	get(ip+"/taxes?section=unknown", 400)

	var conflictedReg string
	err = pool.QueryRow(context.Background(), `
		SELECT q.reg_no FROM published_releases p JOIN tax_regime_quarantine q ON q.release_id = p.release_id
		WHERE p.dataset_code = 'snrip' AND p.period_key = '2026-10-01' ORDER BY q.reg_no LIMIT 1
	`).Scan(&conflictedReg)
	if err != nil {
		t.Fatal(err)
	}
	conflicted := "/reg:" + conflictedReg
	conflictedTaxes := get(conflicted+"/taxes?as_of=2026-10-01", 200)
	if conflictedTaxes["state"] != "identity_conflict" || conflictedTaxes["as_of"] != "2026-10-01" ||
		len(conflictedTaxes["items"].([]any)) != 0 {
		t.Fatal("expected an explicit conflict without withheld regime data")
	}
	var count any
	for _, raw := range conflictedTaxes["sources"].(map[string]any) {
		source := raw.(map[string]any)
		if source["dataset"] == "snrip" && source["period"] == "2026-10-01" {
			count = source["quarantined_count"]
		}
	}
	if count != float64(15) {
		t.Fatalf("expected 15 quarantined documents in the source metadata, got %v", count)
	}
	conflictedProfile := get(conflicted, 200)
	sections := conflictedProfile["sections"].(map[string]any)
	if sections["taxes"].(map[string]any)["state"] != "identity_conflict" ||
		sections["sme"].(map[string]any)["state"] != "found" {
		t.Fatal("expected the SME profile to remain available with the tax conflict")
	}
}
