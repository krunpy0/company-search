package chat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPriorMessagesFitsLatestTurn(t *testing.T) {
	question := strings.Repeat("🙂", 2000)
	summary := strings.Repeat("я", 1499) + "!"
	history := priorMessages([]contextTurn{{Question: question, Summary: summary}})
	if len(history) != 2 || history[0].Role != "user" || history[1].Role != "assistant" {
		t.Fatalf("expected both messages, got %d", len(history))
	}
	if len(history[0].Content)+len(history[1].Content) > 6000 {
		t.Fatal("history exceeds its byte budget")
	}
	for i, original := range []string{question, summary} {
		content := history[i].Content
		if content == "" || !utf8.ValidString(content) || !strings.HasPrefix(original, content) {
			t.Fatalf("message %d is not a valid nonempty prefix", i)
		}
	}
}
