package tools

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/krunpy0/company-search/backend/internal/company"
)

//go:embed schemas.json
var schemaJSON string

type schema struct {
	Properties map[string]property
	Required   []string
}

type property struct {
	Type      string
	Format    string
	Pattern   string
	Enum      []string
	MinLength int
	MaxLength int
	Minimum   int
	Maximum   int
}

var (
	schemas    map[string]schema
	operations = map[string]company.Operation{
		"search_companies":       company.ReadSearch,
		"get_company_profile":    company.ReadProfile,
		"get_company_financials": company.ReadFinancials,
		"get_company_taxes":      company.ReadTaxes,
		"get_company_sources":    company.ReadSources,
	}
)

func init() {
	if err := json.Unmarshal([]byte(schemaJSON), &schemas); err != nil {
		panic(err)
	}
}

func Schemas() json.RawMessage { return json.RawMessage(schemaJSON) }

type Executor struct {
	Reader company.Reader
	Events *Events
}

func (e Executor) Schemas() json.RawMessage {
	if e.Events == nil {
		return Schemas()
	}
	return e.Events.schemas()
}

type ToolResult struct {
	Data     company.Body       `json:"data,omitempty"`
	Error    *company.ReadError `json:"error,omitempty"`
	Warnings []company.Body     `json:"warnings,omitempty"`
}

// The caller supplies userID. Quotas for individual users are a later stage.
func (e Executor) Execute(ctx context.Context, _ string, toolName string, args json.RawMessage) ToolResult {
	if toolName == "get_company_events" && e.Events != nil && len(e.Events.datasets()) != 0 {
		return e.Events.execute(ctx, e.Reader, args)
	}
	operation, ok := operations[toolName]
	if !ok {
		return rejection("unknown_tool", "Unknown company tool")
	}
	params, ok := schemas[toolName].parse(args)
	if !ok {
		return rejection("invalid_arguments", "Arguments do not match the tool schema")
	}
	id := params.Get("id")
	params.Del("id")
	result, err := e.Reader.Read(ctx, operation, id, params)
	if err != nil {
		return rejection("data_unavailable", "Local data temporarily unavailable")
	}
	return ToolResult{Data: result.ToolBody(), Error: result.Error, Warnings: result.Warnings}
}

func rejection(code, message string) ToolResult {
	return ToolResult{Error: &company.ReadError{Code: code, Message: message}}
}

func (s schema) parse(args json.RawMessage) (url.Values, bool) {
	if !utf8.Valid(args) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	params := make(url.Values, len(s.Properties))
	for dec.More() {
		tok, err := dec.Token()
		name, _ := tok.(string)
		field, ok := s.Properties[name]
		if err != nil || !ok || params.Has(name) {
			return nil, false
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) {
			return nil, false
		}
		value, ok := field.parse(raw)
		if !ok {
			return nil, false
		}
		params.Set(name, value)
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	for _, name := range s.Required {
		if !params.Has(name) {
			return nil, false
		}
	}
	return params, true
}

func (p property) parse(raw json.RawMessage) (string, bool) {
	switch p.Type {
	case "integer":
		var n int
		if json.Unmarshal(raw, &n) != nil || n < p.Minimum || n > p.Maximum {
			return "", false
		}
		return strconv.Itoa(n), true
	case "string":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return "", false
		}
		length := utf8.RuneCountInString(value)
		if length < p.MinLength || (p.MaxLength > 0 && length > p.MaxLength) ||
			(len(p.Enum) > 0 && !slices.Contains(p.Enum, value)) {
			return "", false
		}
		if p.Pattern != "" {
			if matched, err := regexp.MatchString(p.Pattern, value); err != nil || !matched {
				return "", false
			}
		}
		if p.Format == "date" {
			if _, err := time.Parse(time.DateOnly, value); err != nil {
				return "", false
			}
		}
		return value, true
	}
	return "", false
}
