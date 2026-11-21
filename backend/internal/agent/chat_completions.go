package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type ChatCompletions struct {
	key, model, endpoint string
}

func NewChatCompletions(baseURL, model, key string) (ChatCompletions, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Hostname() == "" || base.User != nil || base.ForceQuery || base.RawQuery != "" || base.Fragment != "" {
		return ChatCompletions{}, errors.New("задайте корректный OPENAI_BASE_URL")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return ChatCompletions{}, errors.New("задайте OPENAI_MODEL")
	}
	return ChatCompletions{key: strings.TrimSpace(key), model: model, endpoint: base.JoinPath("chat/completions").String()}, nil
}

type functionDefinition struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
}

type functionTool struct {
	Type     string             `json:"type"`
	Function functionDefinition `json:"function"`
}

func (p ChatCompletions) Complete(ctx context.Context, messages []Message, schemas json.RawMessage) (Message, error) {
	var definitions map[string]json.RawMessage
	if err := json.Unmarshal(schemas, &definitions); err != nil || len(definitions) == 0 {
		return Message{}, errors.New("invalid company tool schemas")
	}
	tools := make([]functionTool, 0, len(definitions))
	for _, name := range slices.Sorted(maps.Keys(definitions)) {
		tools = append(tools, functionTool{
			Type:     "function",
			Function: functionDefinition{Name: name, Parameters: definitions[name]},
		})
	}
	body, err := json.Marshal(struct {
		Model     string         `json:"model"`
		Messages  []Message      `json:"messages"`
		Tools     []functionTool `json:"tools"`
		MaxTokens int            `json:"max_tokens"`
	}{
		Model: p.model, Messages: messages, Tools: tools, MaxTokens: 2048,
	})
	if err != nil {
		return Message{}, errors.New("could not encode Chat Completions request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return Message{}, errors.New("could not create Chat Completions request")
	}
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Message{}, ctx.Err()
		}
		return Message{}, errors.New("Chat Completions request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("Chat Completions returned HTTP %d", resp.StatusCode)
	}
	const maxResponse = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return Message{}, errors.New("could not read bounded Chat Completions response")
	}
	var response struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			FinishReason string  `json:"finish_reason"`
			Message      Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Choices) != 1 ||
		(len(response.Error) > 0 && string(response.Error) != "null") {
		return Message{}, errors.New("invalid Chat Completions response")
	}
	choice := response.Choices[0]
	if choice.Message.Role != "assistant" {
		return Message{}, errors.New("invalid Chat Completions assistant message")
	}
	switch choice.FinishReason {
	case "stop":
		if len(choice.Message.ToolCalls) != 0 || strings.TrimSpace(choice.Message.Content) == "" {
			return Message{}, errors.New("invalid Chat Completions final message")
		}
	case "tool_calls":
		if len(choice.Message.ToolCalls) == 0 {
			return Message{}, errors.New("Chat Completions omitted tool calls")
		}
	default:
		return Message{}, errors.New("Chat Completions response did not complete")
	}
	return choice.Message, nil
}
