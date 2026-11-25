package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/krunpy0/company-search/backend/internal/tools"
)

type page struct {
	body    map[string]any
	sources map[string]any
	number  int
	tool    string
}

func newPage(result tools.ToolResult, number int, tool string) (page, error) {
	p := page{number: number, tool: tool}
	raw, err := json.Marshal(result)
	if err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&p.body); err != nil {
		return p, err
	}
	data, _ := p.body["data"].(map[string]any)
	p.sources, _ = data["sources"].(map[string]any)
	delete(data, "sources")
	for _, provider := range []string{"dadata", "checko"} {
		if source, ok := p.sources[provider]; ok {
			p.sources[fmt.Sprintf("%s-%d", provider, number)] = source
		}
	}
	for _, value := range p.sources {
		source, _ := value.(map[string]any)
		if source["dataset"] == "rsmp" {
			source["dataset_name"] = "Единый реестр субъектов малого и среднего предпринимательства"
		}
	}
	if tool == "search_companies" {
		items, _ := data["items"].([]any)
		for _, value := range items {
			item, _ := value.(map[string]any)
			name, _ := item["name"].(map[string]any)
			registration, _ := item["registration_evidence"].(map[string]any)
			if len(registration) > 0 && name["source_ref"] == registration["source_ref"] && name["as_of"] == registration["as_of"] {
				for _, field := range []string{"document_id", "file", "document_date"} {
					if name[field] == registration[field] {
						delete(name, field)
					}
				}
			}
		}
	}
	if tool == "get_company_taxes" && data["state"] == "identity_conflict" {
		for ref, value := range p.sources {
			source, _ := value.(map[string]any)
			if source["dataset"] == "snrip" && source["period"] == data["as_of"] {
				data["state_evidence"] = map[string]any{"source_ref": ref, "as_of": data["as_of"], "state": data["state"]}
				break
			}
		}
	}
	addObservationFields(tool, data)
	return p, nil
}

func (p page) registrations(inn string) []string {
	if (len(inn) != 10 && len(inn) != 12) || strings.Trim(inn, "0123456789") != "" {
		return nil
	}
	data, _ := p.body["data"].(map[string]any)
	items, _ := data["items"].([]any)
	var candidates []string
	for _, value := range items {
		item, _ := value.(map[string]any)
		if id, ok := item["id"].(string); ok && item["inn"] == inn && strings.HasPrefix(id, "reg:") {
			candidates = append(candidates, id)
		}
	}
	return candidates
}

func (p page) emptySearch() bool {
	data, _ := p.body["data"].(map[string]any)
	items, ok := data["items"].([]any)
	coverage, _ := data["coverage"].(map[string]any)
	return ok && len(items) == 0 && data["next_cursor"] == nil &&
		(coverage["dadata"] == "disabled" || coverage["dadata"] == "checked")
}

func (p page) fit(budget int) ([]byte, map[string]Observation, error) {
	data, _ := p.body["data"].(map[string]any)
	for {
		records := make(map[string]Observation)
		catalog := make(map[string]any)
		refs := make(map[string]string)
		delete(data, "sources")
		p.collect(data, "/data", "", records, catalog, refs)
		if data != nil {
			data["sources"] = catalog
		}
		p.body["record_refs"] = refs
		content, err := json.Marshal(p.body)
		if err != nil || len(content) <= budget {
			return content, records, err
		}
		shortened := false
		for _, field := range []string{"items", "observations", "headcounts", "external_suggestions"} {
			if list, ok := data[field].([]any); ok && len(list) > 0 {
				data[field] = list[:len(list)-1]
				shortened = true
				break
			}
		}
		if !shortened {
			return nil, nil, nil
		}
		p.body["truncated"] = true
	}
}

func (p page) collect(value any, path, subject string, records map[string]Observation, sources map[string]any, refs map[string]string) {
	switch node := value.(type) {
	case []any:
		for i, item := range node {
			p.collect(item, path+"/"+strconv.Itoa(i), subject, records, sources, refs)
		}
	case map[string]any:
		if id, ok := node["id"].(string); ok {
			subject = id
		}
		ref, _ := node["source_ref"].(string)
		if ref == "dadata" || ref == "checko" {
			ref = fmt.Sprintf("%s-%d", ref, p.number)
			node["source_ref"] = ref
		}
		source, _ := p.sources[ref].(map[string]any)
		asOf, hasDate := node["as_of"]
		if source != nil && hasDate {
			key := fmt.Sprintf("%d:%s", p.number, path)
			records[key] = Observation{subject, p.tool, ref, asOf, source, node}
			sources[ref] = source
			refs[key] = ref
			return
		}
		for field, child := range node {
			if evidence, ok := child.(map[string]any); field == "registration_evidence" && ok {
				registration := maps.Clone(evidence)
				for _, key := range []string{"inn", "registration_number", "kind"} {
					registration[key] = node[key]
				}
				child = registration
			}
			p.collect(child, path+"/"+field, subject, records, sources, refs)
		}
	}
}

func (p page) findings() map[string]any {
	out := make(map[string]any)
	data, _ := p.body["data"].(map[string]any)
	for _, key := range []string{"state", "reason", "as_of", "years", "sections", "coverage", "warnings", "next_cursor", "next_offset", "next_page", "period"} {
		if value, ok := data[key]; ok {
			out[key] = value
		}
	}
	for _, key := range []string{"error", "warnings", "truncated"} {
		if value, ok := p.body[key]; ok {
			out[key] = value
		}
	}
	return out
}
