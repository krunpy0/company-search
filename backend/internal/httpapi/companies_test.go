package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPublishedCompanyAPI(t *testing.T) {
	dsn := os.Getenv("COMPANY_API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated PostgreSQL with the documented P0 releases")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	router := gin.New()
	RegisterCompanies(router, pool)

	get := func(path string, want int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("got status %d, want %d: %s", w.Code, want, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	const inn = "3907056353"
	const reg = "1073906000376"
	var name, asOf, documentID, file, documentDate, publishedOn string
	err = pool.QueryRow(context.Background(), `
        SELECT s.display_name, f.as_of::text, f.source_doc_id, f.source_file,
               f.document_date::text, rel.published_on::text
        FROM registrations r
        JOIN sme_observations s USING (reg_no)
        JOIN published_releases sp ON sp.release_id = s.release_id
        JOIN financials f ON f.inn = r.inn
        JOIN published_releases fp ON fp.release_id = f.release_id
        JOIN releases rel ON rel.id = f.release_id
        WHERE r.reg_no = $1 AND f.report_year = 2025 AND f.income = 0 AND f.expense = 0`, reg).
		Scan(&name, &asOf, &documentID, &file, &documentDate, &publishedOn)
	if err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{inn, reg} {
		body := get("/v1/companies?q="+url.QueryEscape(q), 200)
		items := body["items"].([]any)
		if len(items) == 0 || items[0].(map[string]any)["id"] != "reg:"+reg {
			t.Fatal("expected the published registration as the first match")
		}
	}
	named := get("/v1/companies?kind=legal&limit=100&q="+url.QueryEscape(name), 200)["items"].([]any)
	if len(named) == 0 || !strings.EqualFold(named[0].(map[string]any)["name"].(map[string]any)["value"].(string), name) {
		t.Fatal("an exact name must rank before similar names")
	}
	found := false
	for _, candidate := range named {
		if candidate.(map[string]any)["id"] == "reg:"+reg {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("name search missed the published registration")
	}
	fuzzy := get("/v1/companies?q="+url.QueryEscape("Гефест"), 200)
	if len(fuzzy["items"].([]any)) == 0 {
		t.Fatal("short name search returned no results")
	}
	base := "/v1/companies/reg:" + reg
	profile := get(base, 200)
	if profile["company"].(map[string]any)["inn"] != inn {
		t.Fatal("unexpected profile INN")
	}
	finance := get(base+"/financials?year=2025", 200)
	item := finance["items"].([]any)[0].(map[string]any)
	if item["income"] != "0.00" || item["expense"] != "0.00" || item["scope"] != "taxpayer" ||
		item["as_of"] != asOf || item["document_date"] != documentDate || item["document_id"] != documentID || item["file"] != file {
		t.Fatal("financial amount, scope or evidence differs from published data")
	}
	source := finance["sources"].(map[string]any)[item["source_ref"].(string)].(map[string]any)
	if source["published_on"] != publishedOn || source["dataset"] != "revexp" {
		t.Fatal("wrong release metadata")
	}
	if len(get(base+"/sources", 200)["observations"].([]any)) < 2 {
		t.Fatal("missing source observations")
	}
	missingYear := get(base+"/financials?year=1900", 200)
	if missingYear["state"] != "not_loaded" || len(missingYear["items"].([]any)) != 0 {
		t.Fatal("missing year must have no amounts")
	}
	get("/v1/companies?q=3907056354", 400)

	var ipINN string
	err = pool.QueryRow(context.Background(), `
        SELECT r.inn
        FROM registrations r JOIN sme_observations s USING (reg_no)
        JOIN published_releases p ON p.release_id = s.release_id
        WHERE r.kind = 'entrepreneur' AND p.dataset_code = 'rsmp' AND p.period_key = 'current'
        GROUP BY r.inn HAVING count(*) > 1 ORDER BY r.inn LIMIT 1`).Scan(&ipINN)
	if err != nil {
		t.Fatal(err)
	}
	search := "/v1/companies?limit=1&q=" + ipINN
	first := get(search, 200)
	firstID := first["items"].([]any)[0].(map[string]any)["id"].(string)
	next := url.QueryEscape(first["next_cursor"].(string))
	second := get(search+"&cursor="+next, 200)
	secondID := second["items"].([]any)[0].(map[string]any)["id"]
	if firstID == secondID {
		t.Fatal("cursor repeated the first item")
	}
	get(search+"&kind=legal&cursor="+next, 400)
	conflict := get("/v1/companies/inn:"+ipINN, 409)["error"].(map[string]any)
	if conflict["code"] != "ambiguous_identity" || len(conflict["candidates"].([]any)) < 2 {
		t.Fatal("expected registration candidates for the ambiguous INN")
	}
	registration := get("/v1/companies?q="+firstID[4:], 200)["items"].([]any)
	if len(registration) != 1 || registration[0].(map[string]any)["id"] != firstID {
		t.Fatal("OGRNIP search did not return the exact registration")
	}
	ipFinance := get("/v1/companies/"+firstID+"/financials", 200)
	if ipFinance["state"] != "not_applicable" || len(ipFinance["items"].([]any)) != 0 {
		t.Fatal("expected finances to be inapplicable for an entrepreneur")
	}

	var financialOnlyINN string
	err = pool.QueryRow(context.Background(), `
		SELECT f.inn
		FROM financials f JOIN published_releases p ON p.release_id = f.release_id
		WHERE p.dataset_code = 'revexp' AND NOT EXISTS (
			SELECT 1 FROM registrations r JOIN sme_observations s USING (reg_no)
			JOIN published_releases sp ON sp.release_id = s.release_id
			WHERE r.inn = f.inn AND sp.dataset_code = 'rsmp' AND sp.period_key = 'current'
		)
		LIMIT 1`).Scan(&financialOnlyINN)
	if err != nil {
		t.Fatal(err)
	}
	financialOnlyID := "inn:" + financialOnlyINN
	only := get("/v1/companies?q="+financialOnlyINN, 200)["items"].([]any)
	if len(only) != 1 || only[0].(map[string]any)["id"] != financialOnlyID {
		t.Fatal("expected taxpayer key without a published registration")
	}
	onlyProfile := get("/v1/companies/"+financialOnlyID, 200)
	if onlyProfile["company"].(map[string]any)["registration_number"] != nil ||
		onlyProfile["sections"].(map[string]any)["sme"].(map[string]any)["state"] != "not_published" {
		t.Fatal("financial records must not imply a registration or published SME membership")
	}

	const absent = "0000000000"
	if len(get("/v1/companies?q="+absent, 200)["items"].([]any)) != 0 {
		t.Fatal("expected an empty result")
	}
	get("/v1/companies/inn:"+absent, 404)
}
