package checko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchUsesFreeBudgetAndFiltersProviderRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/legal-cases" || r.URL.RawQuery != "" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		var params map[string]any
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil ||
			params["key"] != "test-key" || params["inn"] != "7707049388" || params["role"] != "defendant" {
			t.Error("unexpected request body")
		}
		fmt.Fprint(w, `{
			"company":{"ИНН":"7707049388"},
			"data":{"СтрВсего":2,"СтрТекущ":1,"Записи":[
				{"Номер":"А40-123/2025","СтрКАД":"https://kad.arbitr.ru/Card/123","СуммИск":9007199254740993,
				 "Ответ":[{"Адрес":"must not be forwarded"}]}
			]},
			"meta":{"status":"ok","today_request_count":100}
		}`)
	}))
	defer server.Close()
	client, err := New("test-key", 100)
	if err != nil {
		t.Fatal(err)
	}
	client.base = server.URL + "/"
	page, err := client.Fetch(context.Background(), "legal-cases", "inn", "7707049388", "", "defendant", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Next != 2 {
		t.Fatalf("unexpected page: %#v", page)
	}
	item := page.Items[0]
	if len(item) != 4 || item["case_number"] != "А40-123/2025" || item["role_filter"] != "defendant" ||
		item["record_url"] != "https://kad.arbitr.ru/Card/123" || item["claim_amount_rub"] != json.Number("9007199254740993") {
		t.Fatalf("unexpected evidence: %#v", item)
	}
	_, err = client.Fetch(context.Background(), "legal-cases", "inn", "7707049388", "", "defendant", 2, 5)
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("expected quota error, got %v", err)
	}
}

func TestFinancialPageKeepsOnlyPublishedAmountsAndOfficialURL(t *testing.T) {
	var in response
	err := json.Unmarshal([]byte(`{
		"data":{"2025":{"2110":0,"1600":4000},"2024":{"2400":123},"2023":{}},
		"bo.nalog.ru":{"Отчет":{"2025":"https://bo.nalog.gov.ru/download/123","2024":"https://example.org/file"}}
	}`), &in)
	if err != nil {
		t.Fatal(err)
	}
	first, err := financialPage(in, Page{}, 1, 1)
	if err != nil || len(first.Items) != 1 || first.Next != 2 {
		t.Fatalf("unexpected first financial page: %#v %v", first, err)
	}
	if row := first.Items[0]; row["report_year"] != "2025" || row["revenue"] != json.Number("0") ||
		row["net_profit"] != nil || row["record_url"] != "https://bo.nalog.gov.ru/download/123" {
		t.Fatalf("unexpected financial report: %#v", row)
	}
	second, err := financialPage(in, Page{}, 2, 1)
	if err != nil || len(second.Items) != 1 || second.Next != 0 || second.Items[0]["record_url"] != nil {
		t.Fatalf("unexpected second financial page: %#v %v", second, err)
	}
}
