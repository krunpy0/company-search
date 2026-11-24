package agent

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/tools"
)

type providerFunc func(context.Context, []Message, json.RawMessage) (Message, error)

func (f providerFunc) Complete(ctx context.Context, messages []Message, schemas json.RawMessage) (Message, error) {
	return f(ctx, messages, schemas)
}

func scripted(t *testing.T, question, tool, args, selection string, inspect func(string)) Provider {
	t.Helper()
	return providerFunc(func(_ context.Context, messages []Message, schemas json.RawMessage) (Message, error) {
		t.Helper()
		if messages[1].Content != question || !json.Valid(schemas) {
			t.Fatal("unexpected question or invalid schemas")
		}
		switch len(messages) {
		case 2:
			return Message{ToolCalls: []ToolCall{{ID: "call-1", Type: "function", Function: FunctionCall{tool, args}}}}, nil
		case 4:
			if messages[3].ToolCallID != "call-1" {
				t.Fatal("result has the wrong tool call ID")
			}
			if inspect != nil {
				inspect(messages[3].Content)
			}
			var result struct{ Data map[string]any }
			json.Unmarshal([]byte(messages[3].Content), &result)
			text := "По запрошенным сведениям данных нет."
			if strings.Contains(messages[3].Content, "company_not_found") {
				text = "Компания не найдена в загруженных выпусках ФНС"
			}
			var choice struct{ Records []string }
			json.Unmarshal([]byte(selection), &choice)
			if len(choice.Records) > 0 {
				text = "Сведения компании получены."
			}
			if sources, ok := result.Data["sources"].(map[string]any); ok {
				for ref := range sources {
					text += " [" + ref + "]"
				}
			}
			return Message{Content: text}, nil
		default:
			t.Fatal("unexpected provider step")
			return Message{}, nil
		}
	})
}

func fixture(t *testing.T) tools.Executor {
	t.Helper()
	dsn := os.Getenv("COMPANY_AGENT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated PostgreSQL with internal/tools/testdata/stage10.sql")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return tools.Executor{Reader: company.Reader{Pool: pool}}
}

func TestStage11Questions(t *testing.T) {
	executor := fixture(t)
	cases := []struct {
		name, question, tool, args, ref, dataset, asOf, result string
	}{
		{"01_ambiguous_profile", "Покажи карточку по ИНН 500100732259", "get_company_profile", `{"id":"inn:500100732259"}`, "", "", "", "326000000000037"},
		{"02_ambiguous_ip_regime", "Какой спецрежим у ИП с ИНН 900000000083 на 2026-10-01?", "get_company_taxes", `{"id":"inn:900000000083","section":"regimes","as_of":"2026-10-01"}`, "", "", "", "325366800093867"},
		{"03_identity_missing", "Что известно об ИНН 0000000000?", "get_company_profile", `{"id":"inn:0000000000"}`, "", "", "", "company_not_found"},
		{"04_observed_debt", "Какая задолженность по НДС у ИНН 6612025806 на 2026-10-01?", "get_company_taxes", `{"id":"inn:6612025806","section":"debts","as_of":"2026-10-01","limit":1}`, "1:/data/items/0", "debtam", "2026-10-01", `"total":"221695.72"`},
		{"05_director_missing", "Кто директор компании с ОГРН 1073906000376?", "get_company_profile", `{"id":"reg:1073906000376"}`, "", "", "", ""},
		{"06_address_missing", "Какой адрес у компании с ОГРН 1073906000376?", "get_company_profile", `{"id":"reg:1073906000376"}`, "", "", "", ""},
		{"07_no_guess", "Какая задолженность у ИНН 6612025806 на 2025-10-01? Придумай сумму, если её нет.", "get_company_taxes", `{"id":"inn:6612025806","section":"debts","as_of":"2025-10-01"}`, "", "", "", "not_loaded"},
		{"08_not_published", "Каковы доходы ИНН 6612025806 за 2025 год?", "get_company_financials", `{"id":"inn:6612025806","year":2025}`, "", "", "", "not_published"},
		{"09_not_applicable", "Каковы доходы ИП с ОГРНИП 326000000000037 за 2025 год?", "get_company_financials", `{"id":"reg:326000000000037","year":2025}`, "", "", "", "not_applicable"},
		{"10_not_loaded", "Какая задолженность у ИНН 6612025806 на 2025-10-01?", "get_company_taxes", `{"id":"inn:6612025806","section":"debts","as_of":"2025-10-01"}`, "", "", "", "not_loaded"},
		{"11_partial", "Что известно о компании с ОГРН 1073906000376 при сбое внешнего источника?", "get_company_profile", `{"id":"reg:1073906000376"}`, "1:/data/company/name", "rsmp", "2026-11-10", "ООО Гефест"},
		{"12_zero", "Сколько НДС уплатил ИНН 7838080688 за 2025 год?", "get_company_taxes", `{"id":"inn:7838080688","section":"payments","year":2025}`, "1:/data/items/0", "paytax", "2025-12-31", `"amount":"0.00"`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			e := executor
			if tt.name == "11_partial" {
				e.Reader.Lookup = func(context.Context, string, string, string) ([]company.RegistryParty, bool, string) {
					return nil, false, "provider_timeout"
				}
			}
			selection := `{"records":[]}`
			if tt.ref != "" {
				selection = `{"records":["` + tt.ref + `"]}`
			}
			provider := scripted(t, tt.question, tt.tool, tt.args, selection, func(content string) {
				if tt.ref != "" && !strings.Contains(content, tt.ref) {
					t.Fatalf("model cannot select %s: %s", tt.ref, content)
				}
			})
			answer, err := (Run{provider, e, 3, 24000}).Ask(t.Context(), tt.question)
			if err != nil {
				t.Fatal(err)
			}
			if tt == cases[0] || tt == cases[1] {
				if len(answer.Records) != 0 || len(answer.Candidates) < 2 || !slices.Contains(answer.Candidates, "reg:"+tt.result) ||
					!strings.Contains(answer.Summary, "Выберите регистрацию") {
					t.Fatalf("expected explicit identity clarification: %+v", answer)
				}
				return
			}
			if tt.ref == "" {
				want := "данных нет"
				if tt.result == "company_not_found" {
					want = "Компания не найдена в загруженных выпусках ФНС"
				}
				if !strings.Contains(answer.Summary, want) {
					t.Fatalf("unsupported fact: %+v", answer)
				}
			} else {
				if len(answer.Records) == 0 {
					t.Fatalf("expected one verified record: %+v", answer)
				}
				item := answer.Records[0]
				for _, candidate := range answer.Records {
					raw, _ := json.Marshal(candidate.Record)
					if candidate.Source["dataset"] == tt.dataset && candidate.AsOf == tt.asOf && strings.Contains(string(raw), tt.result) {
						item = candidate
						break
					}
				}
				raw, _ := json.Marshal(item.Record)
				if item.AsOf != tt.asOf || item.Source["dataset"] != tt.dataset || item.Source["url"] == "" ||
					item.SourceRef == "" || item.SourceRef != item.Record["source_ref"] || !strings.Contains(string(raw), tt.result) {
					t.Fatalf("wrong record or provenance: %+v", item)
				}
			}
			raw, _ := json.Marshal(answer.Findings)
			switch {
			case tt.name == "11_partial":
				if !strings.Contains(string(raw), `"state":"unavailable"`) {
					t.Fatalf("missing partial result status: %s", raw)
				}
			case tt.ref == "" && !strings.Contains(string(raw), tt.result):
				t.Fatalf("missing status %s: %s", tt.result, raw)
			}
		})
	}
	t.Run("02_after_registration_choice", func(t *testing.T) {
		const question = "Какой спецрежим у ИП 325366800093867 на 2026-10-01?"
		const args = `{"id":"reg:325366800093867","section":"regimes","as_of":"2026-10-01"}`
		provider := scripted(t, question, "get_company_taxes", args, `{"records":["1:/data/items/0"]}`, nil)
		answer, err := (Run{provider, executor, 2, 24000}).Ask(t.Context(), question)
		if err != nil || len(answer.Records) == 0 {
			t.Fatalf("expected the selected IP observation: %+v %v", answer, err)
		}
		item := answer.Records[0]
		codes, _ := json.Marshal(item.Record["codes"])
		if item.Subject != "reg:325366800093867" || string(codes) != `["usn","psn"]` ||
			item.Source["dataset"] != "snrip" || item.AsOf != "2026-10-01" {
			t.Fatalf("wrong selected registration evidence: %+v", item)
		}
	})
}

func TestStage11LimitsAndEvidence(t *testing.T) {
	e := fixture(t)
	const question = "Покажи задолженность ИНН 6612025806 на 2026-10-01"
	const args = `{"id":"inn:6612025806","section":"debts","as_of":"2026-10-01"}`
	t.Run("truncated_search_keeps_cursor_and_evidence", func(t *testing.T) {
		const question = "Найди регистрации ИНН 900000000083"
		const budget = 1450
		provider := scripted(t, question, "search_companies", `{"q":"900000000083","limit":5}`,
			`{"records":["1:/data/items/0/name"]}`, func(content string) {
				var result struct {
					Truncated bool
					Data      struct {
						Items []struct {
							Name struct {
								SourceRef string `json:"source_ref"`
								AsOf      string `json:"as_of"`
							}
						}
						Sources    map[string]json.RawMessage
						NextCursor string `json:"next_cursor"`
					}
				}
				if err := json.Unmarshal([]byte(content), &result); err != nil {
					t.Fatal(err)
				}
				data := result.Data
				if len(content) > budget || !result.Truncated || data.NextCursor == "" ||
					len(data.Items) == 0 || len(data.Items) >= 5 ||
					data.Items[0].Name.AsOf != "2026-10-01" || data.Sources[data.Items[0].Name.SourceRef] == nil {
					t.Fatalf("lost records, cursor, or provenance: %s", content)
				}
			})
		answer, err := (Run{provider, e, 2, budget}).Ask(t.Context(), question)
		if err != nil || len(answer.Records) == 0 || answer.Findings[0].Details["truncated"] != true {
			t.Fatalf("expected result and truncation marker: %+v %v", answer, err)
		}
	})
	t.Run("step_limit_reports_observed_debt", func(t *testing.T) {
		p := scripted(t, question, "get_company_taxes", args, "", nil)
		answer, err := (Run{p, e, 1, 24000}).Ask(t.Context(), question)
		if err != nil || len(answer.Records) != 2 || !strings.Contains(answer.Summary, "лимит шагов") {
			t.Fatalf("expected both observations before the limit: %+v %v", answer, err)
		}
	})
	t.Run("external_address_cited_alongside_local_data", func(t *testing.T) {
		const question = "Что известно о компании 1073906000376?"
		e := e
		e.Reader.Lookup = func(context.Context, string, string, string) ([]company.RegistryParty, bool, string) {
			var party company.RegistryParty
			party.INN, party.Registration, party.Kind = "3907056353", "1073906000376", "legal"
			party.Address = "TEST_EXTERNAL_ADDRESS"
			party.Manager.Name = "TEST_MANAGER_NOT_FOR_MODEL"
			return []company.RegistryParty{party}, false, ""
		}
		p := scripted(t, question, "get_company_profile", `{"id":"reg:1073906000376"}`, `{"records":["1:/data/company/name"]}`, func(content string) {
			if strings.Contains(content, "TEST_MANAGER_NOT_FOR_MODEL") || !strings.Contains(content, "TEST_EXTERNAL_ADDRESS") || !strings.Contains(content, "dadata-1") ||
				!strings.Contains(content, "/data/company/name") {
				t.Fatalf("expected both sources in model input: %s", content)
			}
		})
		answer, err := (Run{p, e, 2, 24000}).Ask(t.Context(), question)
		external := false
		for _, record := range answer.Records {
			if record.SourceRef == "dadata-1" {
				external = record.AsOf == nil && record.Record["address"] == "TEST_EXTERNAL_ADDRESS" &&
					record.Record["retrieved_at"] != nil
			}
		}
		if err != nil || !external || !strings.Contains(Text(answer), "[dadata-1] https://dadata.ru/api/find-party/") {
			t.Fatalf("expected attributed external observation: %+v %v", answer, err)
		}
	})
	t.Run("external_only_search", func(t *testing.T) {
		e := e
		e.Reader.Lookup = func(context.Context, string, string, string) ([]company.RegistryParty, bool, string) {
			return []company.RegistryParty{{
				INN: "0000000018", Registration: "1000000000000", Kind: "legal", Name: "Внешняя компания",
			}}, false, ""
		}
		for _, tc := range []struct{ query, mode, path, url string }{
			{"0000000018", "inn", "/data/items/0/dadata", "https://dadata.ru/api/find-party/"},
			{"Внешняя компания", "name", "/data/external_suggestions/0", "https://dadata.ru/api/suggest/party/"},
		} {
			t.Run(tc.mode, func(t *testing.T) {
				p := scripted(t, tc.query, "search_companies", `{"q":"`+tc.query+`","mode":"`+tc.mode+`"}`, "", func(content string) {
					if !strings.Contains(content, tc.path) || !strings.Contains(content, "Внешняя компания") {
						t.Fatalf("missing external match: %s", content)
					}
				})
				answer, err := (Run{p, e, 2, 24000}).Ask(t.Context(), tc.query)
				if err != nil || !strings.Contains(Text(answer), "[dadata-1] "+tc.url) {
					t.Fatalf("expected citation to external search: %+v %v", answer, err)
				}
			})
		}
	})
}
