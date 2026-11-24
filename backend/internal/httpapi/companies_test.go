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
	"github.com/krunpy0/company-search/backend/internal/company"
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
	RegisterCompanies(router, pool, nil, false)

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

	main := company.RegistryParty{INN: inn, Registration: reg, Kind: "legal", Name: "DaData fixture",
		Address: "Test legal address", LastChangedOn: "2025-01-01",
		Manager: company.RegistryManager{Name: "Test manager", Title: "Director", StartedOn: "2024-01-01"},
	}
	branch := main
	branch.Kind = ""
	otherINN := main
	otherINN.INN = "7707083893"
	const externalINN = "000000000000"
	ip := company.RegistryParty{INN: externalINN, Registration: "326000000000015", Kind: "entrepreneur", Name: "ИП Тест", ResidenceCity: "Test city", Address: "Private address fixture"}
	ip2 := ip
	ip2.Registration = "326000000000026"
	var warning string
	var conflictingProvider bool
	router = gin.New()
	lookup := func(_ context.Context, query, mode, _ string) ([]company.RegistryParty, bool, string) {
		switch {
		case warning != "":
			return nil, false, warning
		case query == externalINN:
			return []company.RegistryParty{ip, ip2}, false, ""
		case query == ip.Registration:
			return []company.RegistryParty{ip, ip2}, false, ""
		case mode == "name" || query == inn:
			return []company.RegistryParty{branch, main, main}, false, ""
		case query == reg && conflictingProvider:
			return []company.RegistryParty{otherINN}, false, ""
		case query == reg:
			return []company.RegistryParty{main}, false, ""
		default:
			return []company.RegistryParty{}, false, ""
		}
	}
	RegisterCompanies(router, pool, lookup, true)
	merged := get("/v1/companies?q="+inn, 200)
	mergedItems := merged["items"].([]any)
	firstMatch := mergedItems[0].(map[string]any)
	if len(mergedItems) != 1 || firstMatch["name"].(map[string]any)["value"] != name ||
		firstMatch["dadata"].(map[string]any)["name"] != main.Name ||
		merged["warnings"].([]any)[0].(map[string]any)["code"] != "provider_name_differs" {
		t.Fatal("expected a single matching registration with both observations")
	}
	nameSearch := get("/v1/companies?q=fixture-search", 200)
	suggestions, _ := nameSearch["external_suggestions"].([]any)
	if len(suggestions) != 1 ||
		nameSearch["coverage"].(map[string]any)["dadata"] != "limited" {
		t.Fatalf("expected one separate suggestion and limited coverage: %+v", nameSearch)
	}
	exact := get("/v1/companies?q="+reg, 200)["items"].([]any)
	if len(exact) != 1 {
		t.Fatalf("expected exact legal registration: %+v", exact)
	}
	registryProfile := get(base, 200)["dadata"].(map[string]any)
	manager := registryProfile["manager"].(map[string]any)
	if registryProfile["address"] != main.Address || registryProfile["last_changed_on"] != main.LastChangedOn ||
		registryProfile["source"] != "dadata" || registryProfile["retrieved_at"] == nil || registryProfile["as_of"] != nil ||
		manager["name"] != main.Manager.Name || manager["title"] != main.Manager.Title ||
		manager["started_on"] != main.Manager.StartedOn {
		t.Fatalf("unexpected legal registry details: %+v", registryProfile)
	}
	if suggestions[0].(map[string]any)["manager"] != nil {
		t.Fatal("name suggestions must not include new registry details")
	}
	router = gin.New()
	RegisterCompanies(router, pool, lookup, false)
	baseline := get(base, 200)["dadata"].(map[string]any)
	if baseline["manager"] != nil || baseline["address"] != main.Address {
		t.Fatalf("unexpected registry fields with enrichment disabled: %+v", baseline)
	}
	router = gin.New()
	RegisterCompanies(router, pool, lookup, true)
	conflictingProvider = true
	rejected := get(base, 200)
	if rejected["sections"].(map[string]any)["registration"].(map[string]any)["state"] != "identity_conflict" {
		t.Fatal("profile coverage must report an identity conflict")
	}
	if rejected["dadata"] != nil || rejected["warnings"].([]any)[0].(map[string]any)["code"] != "provider_identity_conflict" {
		t.Fatal("conflicting provider identity must not be attached to the local profile")
	}
	if len(get("/v1/companies?q="+externalINN, 200)["items"].([]any)) != 2 {
		t.Fatal("expected two external registrations for the same INN")
	}
	get("/v1/companies/inn:"+externalINN, 409)
	external := get("/v1/companies/reg:"+ip.Registration, 200)
	ipDetails := external["dadata"].(map[string]any)
	if ipDetails["id"] != "reg:"+ip.Registration || ipDetails["residence_city"] != ip.ResidenceCity ||
		ipDetails["address"] != nil || ipDetails["manager"] != nil {
		t.Fatal("expected an exact external registration in the profile")
	}
	if len(get("/v1/companies?q="+absent, 200)["items"].([]any)) != 0 {
		t.Fatal("expected an empty result after both sources responded")
	}
	warning = "provider_timeout"
	degraded := get(base, 200)
	if degraded["company"].(map[string]any)["inn"] != inn ||
		degraded["coverage"].(map[string]any)["dadata"] != "unavailable" ||
		degraded["warnings"].([]any)[0].(map[string]any)["code"] != warning {
		t.Fatal("provider failure must preserve the local profile and explain degraded coverage")
	}
	get("/v1/companies/inn:"+absent, 503)
	var foundCount int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM registrations WHERE inn = $1", externalINN).Scan(&foundCount); err != nil || foundCount != 0 {
		t.Fatalf("external registrations must stay out of PostgreSQL: count=%d, err=%v", foundCount, err)
	}
}
