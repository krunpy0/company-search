package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatCompletionsToolRoundTrip(t *testing.T) {
	const schemas = `{"get_company_profile":{"description":"Company profile","type":"object","required":["id"]}}`
	const details = `[{"type":"reasoning.encrypted","data":"opaque","index":0}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/custom/v1/chat/completions" ||
			r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("Chat Completions request method, URL or headers differ from the chat API")
		}
		var request struct {
			Model     string          `json:"model"`
			Messages  []Message       `json:"messages"`
			Tools     json.RawMessage `json:"tools"`
			MaxTokens int             `json:"max_tokens"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		const wireTools = `[{"type":"function","function":{"name":"get_company_profile","description":"Company profile","parameters":{"description":"Company profile","type":"object","required":["id"]}}}]`
		if string(request.Tools) != wireTools || request.MaxTokens <= 0 {
			t.Error("expected the generated tool schema, token cap")
		}
		if len(request.Messages) == 1 {
			if request.Model != "test/model" || request.Messages[0].Content != "Карточка" {
				t.Errorf("unexpected first request: %+v", request)
			}
			io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning_details":`+details+`,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_company_profile","arguments":"{\"id\":\"reg:1073906000376\"}"}}]}}]}`)
			return
		}
		if request.Model != "test/model" || len(request.Messages) != 3 ||
			string(request.Messages[1].ReasoningDetails) != details ||
			request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "call_1" ||
			request.Messages[2].Content != `{"data":{"id":"reg:1073906000376"}}` {
			t.Errorf("unexpected follow-up request: %+v", request)
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"facts\":[]}"}}]}`)
	}))
	defer server.Close()

	ctx := context.Background()
	p, err := NewChatCompletions(server.URL+"/custom/v1/", "test/model", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	messages := []Message{{Role: "user", Content: "Карточка"}}
	assistant, err := p.Complete(ctx, messages, json.RawMessage(schemas))
	if err != nil || len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v, %v", assistant, err)
	}
	call := assistant.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "get_company_profile" || call.Function.Arguments != `{"id":"reg:1073906000376"}` {
		t.Fatalf("unexpected tool call: %+v", call)
	}
	messages = append(messages, assistant, Message{
		Role: "tool", ToolCallID: call.ID, Content: `{"data":{"id":"reg:1073906000376"}}`,
	})

	final, err := p.Complete(ctx, messages, json.RawMessage(schemas))
	if err != nil || final.Content != `{"facts":[]}` {
		t.Fatalf("expected final content, got %+v, %v", final, err)
	}
}

func TestChatCompletionsRejectsIncompleteResponse(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"upstream error", 429, "do not expose upstream response"},
		{"oversized", 200, strings.Repeat("x", (1<<20)+1)},
		{"truncated generation", 200, `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"partial"}}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("unexpected Authorization header")
				}
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer server.Close()
			p, err := NewChatCompletions(server.URL, "local-model", "")
			if err != nil {
				t.Fatal(err)
			}
			message, err := p.Complete(context.Background(), []Message{{Role: "user", Content: "test"}}, json.RawMessage(`{"lookup":{"type":"object"}}`))
			if err == nil || strings.Contains(err.Error(), tt.body) || message.Content != "" {
				t.Fatalf("expected a sanitized error and empty message, got %+v, %v", message, err)
			}
		})
	}
}
