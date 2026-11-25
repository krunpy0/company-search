package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/tools"
)

type toolFunc func(string, json.RawMessage) tools.ToolResult

func (f toolFunc) Execute(_ context.Context, _ string, name string, args json.RawMessage) tools.ToolResult {
	return f(name, args)
}

func TestConversationKeepsToolsAndFollowUp(t *testing.T) {
	calls := 0
	executor := toolFunc(func(name string, args json.RawMessage) tools.ToolResult {
		calls++
		if !strings.Contains(string(args), "7415081779") {
			t.Fatalf("lost company: %s", args)
		}
		return tools.ToolResult{Data: company.Body{
			"id":      "inn:7415081779",
			"value":   company.Body{"value": "ПРОФИЛЬ МАКС", "source_ref": "fns-3", "as_of": "2026-11-10"},
			"sources": company.Body{"fns-3": company.Body{"url": "https://example.test/source"}},
		}}
	})
	step := 0
	provider := providerFunc(func(_ context.Context, messages []Message, _ json.RawMessage) (Message, error) {
		step++
		switch step {
		case 1:
			return Message{ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: FunctionCall{"get_company_profile", `{"id":"inn:7415081779"}`}}}}, nil
		case 2:
			return Message{Content: "ПРОФИЛЬ МАКС. Данные на 2026-11-10 [fns-3]."}, nil
		case 3:
			if len(messages) != 6 || messages[3].Role != "tool" || messages[5].Content != "А доходы?" {
				t.Fatalf("lost history: %+v", messages)
			}
			return Message{ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: FunctionCall{"get_company_financials", `{"id":"inn:7415081779","year":2025}`}}}}, nil
		default:
			return Message{Content: "Финансовые сведения [fns-3]."}, nil
		}
	})
	session := &Session{Run: Run{provider, executor, 4, 24000}}
	first, err := session.Ask(t.Context(), "7415081779 расскажи про компанию")
	if err != nil || !strings.Contains(Text(first), "ПРОФИЛЬ МАКС") || !strings.Contains(Text(first), "https://example.test/source") {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := session.Ask(t.Context(), "А доходы?")
	if err != nil || calls != 2 || len(second.Records) != 2 {
		t.Fatalf("%+v %v calls=%d", second, err, calls)
	}
}

func TestCitationsAndFreeText(t *testing.T) {
	records := map[string]Observation{"1": {SourceRef: "fns-3"}}
	for _, text := range []string{"Компания [fns-9]", "Компания [fns-3, fns-9]", `{"records":[]}`, "Компания без источника"} {
		if checkCitations(text, records) == nil {
			t.Fatalf("accepted %s", text)
		}
	}
	if err := checkCitations("Компания [fns-3]", records); err != nil {
		t.Fatal(err)
	}
}

func TestTextResolvesGroupedCitations(t *testing.T) {
	output := Text(Answer{
		Summary: "Сотрудники [fns-3, fns-7]. МСП [fns-3].",
		Records: []Observation{
			{SourceRef: "fns-3", Source: map[string]any{"url": "https://example.test/rsmp"}},
			{SourceRef: "fns-7", Source: map[string]any{"url": "https://example.test/headcount"}},
		},
	})
	if strings.Count(output, "https://example.test/rsmp") != 1 || !strings.Contains(output, "[fns-7] https://example.test/headcount") {
		t.Fatal(output)
	}
}

func TestRevexpDifferenceUsesExactDecimalAmounts(t *testing.T) {
	item := map[string]any{"income": "9007199254740992.01", "expense": "9007199254740992.02"}
	addObservationFields("get_company_financials", map[string]any{"items": []any{item}})
	if item["income_expense_difference"] != "-0.01" {
		t.Fatalf("incorrect difference: %v", item)
	}
}

func TestOKVEDLineUsesOnlyObservedCode(t *testing.T) {
	records := map[string]Observation{"1": {SourceRef: "fns-3", Record: map[string]any{"okved": "35.3"}}}
	text := "Компания [fns-3]\n- **Код OKVED**: 35.3 (управление зданиями) [fns-7]\n- ОКВЭД 86.23\nРегион 54"
	want := "Компания [fns-3]\n- ОКВЭД 35.3 [fns-3]\n- Код ОКВЭД не подтверждён.\nРегион 54"
	if got := formatOKVEDLines(text, records); got != want {
		t.Fatalf("unexpected OKVED text: %s", got)
	}
}

func TestRevexpDifferenceIsNotPresentedAsProfit(t *testing.T) {
	requests := 0
	provider := providerFunc(func(_ context.Context, messages []Message, _ json.RawMessage) (Message, error) {
		requests++
		switch requests {
		case 1:
			return Message{ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: FunctionCall{"get_company_financials", `{"id":"inn:0522014502","year":2025}`}}}}, nil
		case 2:
			return Message{Content: "Оценочная прибыль составила 280 000 [fns-1]."}, nil
		default:
			return Message{Content: "Разница доходов и расходов составила 280 000 [fns-1]."}, nil
		}
	})
	executor := toolFunc(func(string, json.RawMessage) tools.ToolResult {
		return tools.ToolResult{Data: company.Body{
			"items":   []company.Body{{"income": "10513000.00", "expense": "10233000.00", "source_ref": "fns-1", "as_of": "2025-12-31"}},
			"sources": company.Body{"fns-1": company.Body{"dataset": "revexp"}},
		}}
	})
	answer, err := (Run{provider, executor, 3, 24000}).Ask(t.Context(), "Какая прибыль в 2025 году?")
	if err != nil || answer.Partial || requests != 3 || strings.Contains(answer.Summary, "Оценочная прибыль") ||
		!strings.Contains(answer.Summary, profitNotConfirmed) || !strings.Contains(answer.Summary, "Разница доходов и расходов составила 280 000") {
		t.Fatalf("accepted an unsupported profit estimate: %+v, %v", answer, err)
	}
}

func TestRegistrationChoiceRequiresUserInput(t *testing.T) {
	const question = "Покажи карточку по ИНН 010502131268"
	const selected = "reg:324237500064726"
	candidates := []string{"reg:316237500058969", selected}
	for _, firstTool := range []string{"get_company_profile", "search_companies"} {
		t.Run(firstTool, func(t *testing.T) {
			requests, reads := 0, 0
			provider := providerFunc(func(_ context.Context, messages []Message, _ json.RawMessage) (Message, error) {
				requests++
				profileCall := func(id string) Message {
					return Message{ToolCalls: []ToolCall{{ID: id, Type: "function", Function: FunctionCall{"get_company_profile", `{"id":"` + id + `"}`}}}}
				}
				switch requests {
				case 1:
					if firstTool == "search_companies" {
						return Message{ToolCalls: []ToolCall{{ID: "search", Type: "function", Function: FunctionCall{firstTool, `{"q":"010502131268"}`}}}}, nil
					}
					return profileCall("inn:010502131268"), nil
				case 2:
					return profileCall(candidates[0]), nil
				default:
					if !slices.ContainsFunc(messages, func(message Message) bool {
						return message.Role == "user" && strings.Contains(message.Content, question) && strings.Contains(message.Content, selected)
					}) {
						t.Fatal("lost the original question or the user's choice")
					}
					if requests == 3 && firstTool == "search_companies" {
						return profileCall(candidates[0]), nil
					}
					if messages[len(messages)-1].Role == "tool" && strings.Contains(messages[len(messages)-1].Content, "registration_selection_required") {
						return profileCall(selected), nil
					}
					return Message{Content: "Карточка выбранной регистрации"}, nil
				}
			})
			executor := toolFunc(func(name string, args json.RawMessage) tools.ToolResult {
				reads++
				if reads == 1 && name == "search_companies" {
					return tools.ToolResult{Data: company.Body{"items": []company.Body{
						{"id": candidates[0], "inn": "010502131268"}, {"id": selected, "inn": "010502131268"},
					}}}
				}
				if reads == 1 {
					return tools.ToolResult{Error: &company.ReadError{Code: "ambiguous_identity", Candidates: candidates}}
				}
				if !strings.Contains(string(args), selected) {
					t.Fatalf("read the wrong registration: %s", args)
				}
				return tools.ToolResult{Data: company.Body{"id": selected}}
			})
			s := &Session{Run: Run{provider, executor, 4, 24000}}
			for _, input := range []string{question, "305010506000010", "А какой у него режим?"} {
				answer, err := s.Ask(t.Context(), input)
				if err != nil || !slices.Equal(answer.Candidates, candidates) || reads != 1 {
					t.Fatalf("resolved before a valid selection: %+v, %v; reads=%d", answer, err, reads)
				}
			}
			answer, err := s.Ask(t.Context(), strings.TrimPrefix(selected, "reg:"))
			if err != nil || answer.Partial || len(answer.Candidates) != 0 || reads != 2 || s.pending != nil {
				t.Fatalf("did not resume the selected registration: %+v, %v; reads=%d", answer, err, reads)
			}
		})
	}
}

func TestFailedTurnDoesNotPoisonHistory(t *testing.T) {
	s := &Session{Run: Run{Provider: providerFunc(func(context.Context, []Message, json.RawMessage) (Message, error) {
		return Message{}, errors.New("offline")
	}), Steps: 2, Bytes: 24000}}
	if _, err := s.Ask(t.Context(), "вопрос"); err == nil {
		t.Fatal("expected failure")
	}
	if len(s.messages) != 0 {
		t.Fatal("failed turn committed")
	}
}

func TestTimeoutStillShowsToolStatus(t *testing.T) {
	provider := providerFunc(func(_ context.Context, messages []Message, _ json.RawMessage) (Message, error) {
		if messages[len(messages)-1].Role == "tool" {
			return Message{}, errModelTimeout
		}
		return Message{ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: FunctionCall{"get_company_taxes", `{"id":"inn:6612025806","section":"debts"}`}}}}, nil
	})
	executor := toolFunc(func(string, json.RawMessage) tools.ToolResult {
		return tools.ToolResult{Data: company.Body{"state": "not_loaded"}}
	})
	answer, err := (Run{provider, executor, 2, 24000}).Ask(t.Context(), "Какая задолженность?")
	if err != nil || !answer.Partial || !strings.Contains(answer.Summary, "ожидания ответа модели") || !strings.Contains(Text(answer), "not_loaded") {
		t.Fatalf("lost timeout or completed tool status: %+v, %v", answer, err)
	}
}

func TestEmptyExactSearchReportsOnlyNoMatch(t *testing.T) {
	provider := providerFunc(func(_ context.Context, messages []Message, _ json.RawMessage) (Message, error) {
		if messages[len(messages)-1].Role == "tool" {
			t.Fatal("sent the empty result to the model for interpretation")
		}
		return Message{ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: FunctionCall{"search_companies", `{"q":"7707083893"}`}}}}, nil
	})
	executor := toolFunc(func(string, json.RawMessage) tools.ToolResult {
		return tools.ToolResult{Data: company.Body{"items": []any{}, "coverage": company.Body{"dadata": "disabled"}}}
	})
	answer, err := (Run{provider, executor, 2, 24000}).Ask(t.Context(), "Что известно о компании с ИНН 7707083893?")
	if err != nil || answer.Summary != "По номеру 7707083893 совпадений в доступных источниках не найдено." || len(answer.Findings) != 1 {
		t.Fatalf("unexpected exact search result: %+v, %v", answer, err)
	}
}

func TestTaxRegimeConflictNamesTheTaxSource(t *testing.T) {
	provider := providerFunc(func(_ context.Context, messages []Message, _ json.RawMessage) (Message, error) {
		if messages[len(messages)-1].Role == "tool" {
			return Message{Content: "Конфликт в реестре МСП [fns-3]."}, nil
		}
		return Message{ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: FunctionCall{"get_company_taxes", `{"id":"reg:304050433600030","section":"regimes"}`}}}}, nil
	})
	executor := toolFunc(func(string, json.RawMessage) tools.ToolResult {
		return tools.ToolResult{Data: company.Body{
			"state": "identity_conflict", "as_of": "2026-10-01",
			"sources": company.Body{"fns-10": company.Body{"dataset": "snrip", "period": "2026-10-01", "url": "https://example.test/snrip"}},
		}}
	})
	answer, err := (Run{provider, executor, 2, 24000}).Ask(t.Context(), "Какой спецрежим у ИП 304050433600030?")
	if err != nil || !answer.Partial || !strings.Contains(answer.Summary, "2026-10-01") ||
		!strings.Contains(Text(answer), "[fns-10] https://example.test/snrip") || strings.Contains(answer.Summary, "МСП") {
		t.Fatalf("wrong conflict provenance: %+v, %v", answer, err)
	}
}

func TestUnknownCitationIsRepairedBeforeDisplay(t *testing.T) {
	calls := 0
	s := &Session{Run: Run{Provider: providerFunc(func(_ context.Context, messages []Message, _ json.RawMessage) (Message, error) {
		calls++
		if calls == 1 {
			return Message{Content: "Выдуманный источник [fns-99]"}, nil
		}
		if !strings.Contains(messages[len(messages)-1].Content, "отсутствует") {
			t.Fatal("missing correction request")
		}
		return Message{Content: "Подтверждённых сведений пока нет."}, nil
	}), Steps: 2, Bytes: 24000}}
	answer, err := s.Ask(t.Context(), "Что известно?")
	if err != nil || calls != 2 || strings.Contains(Text(answer), "fns-99") || answer.Partial {
		t.Fatalf("%+v %v", answer, err)
	}
}
