package agent

import (
	"context"
	"encoding/json"
	"errors"
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
