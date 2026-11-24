package agent

import (
	"testing"

	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/tools"
)

func TestCheckoEventsRetainCitableEvidenceWithUnknownAsOf(t *testing.T) {
	result := tools.ToolResult{Data: company.Body{
		"id": "inn:7707049388",
		"items": []company.Body{{
			"source_ref": "checko", "as_of": nil, "case_number": "А40-123/2025",
		}},
		"sources": company.Body{
			"checko": company.Body{"url": "https://checko.ru/integration/api/legal-cases"},
		},
	}}
	page, err := newPage(result, 2, "get_company_events")
	if err != nil {
		t.Fatal(err)
	}
	_, observations, err := page.fit(1000)
	if err != nil {
		t.Fatal(err)
	}
	record := observations["2:/data/items/0"]
	if record.SourceRef != "checko-2" || record.Subject != "inn:7707049388" || record.AsOf != nil {
		t.Fatalf("unexpected evidence: %#v", record)
	}
	if err := checkCitations("Найдено дело [checko-2].", observations); err != nil {
		t.Fatal(err)
	}
	if err := checkCitations("Найдено дело [checko-1].", observations); err == nil {
		t.Fatal("unobserved source was accepted")
	}
}
