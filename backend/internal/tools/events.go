package tools

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/krunpy0/company-search/backend/internal/checko"
	"github.com/krunpy0/company-search/backend/internal/company"
)

type Events struct {
	Support     bool
	TaxOffences bool
	Checko      *checko.Client
}

func (e Events) datasets() []string {
	var out []string
	if e.Support {
		out = append(out, "rsmppp")
	}
	if e.TaxOffences {
		out = append(out, "taxoffence")
	}
	if e.Checko != nil {
		out = append(out, "legal-cases", "enforcements", "inspections", "contracts", "finances",
			"fedresurs", "bankruptcy-messages", "timeline")
	}
	return out
}

func (e Events) schemas() json.RawMessage {
	if len(e.datasets()) == 0 {
		return Schemas()
	}
	var advertised map[string]json.RawMessage
	if err := json.Unmarshal(Schemas(), &advertised); err != nil {
		panic(err)
	}
	advertised["get_company_events"] = e.contract()
	raw, err := json.Marshal(advertised)
	if err != nil {
		panic(err)
	}
	return raw
}

func (e Events) contract() json.RawMessage {
	fields := map[string]any{
		"id":      map[string]any{"type": "string", "pattern": `^(inn:[0-9]{10}|reg:([0-9]{13}|[0-9]{15}))$`},
		"dataset": map[string]any{"type": "string", "enum": e.datasets()},
		"page":    map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
		"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
	}
	if e.Checko != nil {
		fields["law"] = map[string]any{"type": "string", "enum": []string{"44", "94", "223"}}
		fields["role"] = map[string]any{"type": "string", "enum": []string{"plaintiff", "defendant", "supplier", "customer"}}
	}
	raw, err := json.Marshal(map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"id", "dataset"}, "properties": fields,
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func (e Events) execute(ctx context.Context, reader company.Reader, args json.RawMessage) ToolResult {
	var contract schema
	if err := json.Unmarshal(e.contract(), &contract); err != nil {
		panic(err)
	}
	params, ok := contract.parse(args)
	if !ok {
		return rejection("invalid_arguments", "Arguments do not match the enabled event sources")
	}
	id, dataset := params.Get("id"), params.Get("dataset")
	mode, number, invalid := company.EventSubject(id)
	if invalid != nil {
		return ToolResult{Error: invalid}
	}
	page, _ := strconv.Atoi(params.Get("page"))
	limit, _ := strconv.Atoi(params.Get("limit"))
	page, limit = max(1, page), max(1, limit)
	if !params.Has("limit") {
		limit = 5
	}
	law, role := params.Get("law"), params.Get("role")
	switch dataset {
	case "contracts":
		if law == "" || (role != "" && !slices.Contains([]string{"supplier", "customer"}, role)) {
			return rejection("invalid_arguments", "Contracts require law 44, 94 or 223 and accept supplier/customer")
		}
	case "legal-cases":
		if law != "" || (role != "" && !slices.Contains([]string{"plaintiff", "defendant"}, role)) {
			return rejection("invalid_arguments", "Arbitration cases accept plaintiff/defendant only")
		}
	default:
		if law != "" || role != "" {
			return rejection("invalid_arguments", "This dataset does not accept law or role")
		}
	}
	if dataset == "rsmppp" || dataset == "taxoffence" {
		out, err := reader.FNSEvents(ctx, id, dataset, page, limit)
		if err != nil {
			return rejection("data_unavailable", "Local event data temporarily unavailable")
		}
		return ToolResult{Data: out.ToolBody(), Error: out.Error}
	}
	body := company.Body{"id": id, "dataset": dataset, "state": "not_applicable"}
	if (dataset == "enforcements" || dataset == "finances") && len(number) == 15 {
		return ToolResult{Data: body}
	}
	result, err := e.Checko.Fetch(ctx, dataset, mode, number, law, role, page, limit)
	if err != nil {
		body["state"] = "unavailable"
		if errors.Is(err, checko.ErrQuota) {
			body["reason"] = "Checko daily request limit reached"
		} else {
			body["reason"] = "Checko could not return a verified page"
		}
		return ToolResult{Data: body}
	}
	body["state"] = "not_published"
	if len(result.Items) > 0 {
		body["state"] = "found"
	}
	for _, item := range result.Items {
		item["dataset"] = dataset
		item["source_ref"] = "checko"
		item["as_of"] = nil
		item["retrieved_at"] = result.Retrieved.Format(time.RFC3339)
	}
	if result.Next > 0 {
		body["next_page"] = result.Next
	}
	body["items"] = result.Items
	body["sources"] = company.Body{"checko": company.Body{
		"publisher": "Чекко", "dataset": dataset, "url": "https://checko.ru/integration/api/" + dataset,
	}}
	return ToolResult{Data: body}
}
