package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"

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
	delete(data, "dadata")
	delete(data, "external_suggestions")
	if items, ok := data["items"].([]any); ok && tool == "search_companies" {
		local := items[:0]
		for _, value := range items {
			item := value.(map[string]any)
			_, external := item["dadata"]
			delete(item, "dadata")
			if !external || item["registration_evidence"] != nil || item["name"] != nil {
				local = append(local, item)
			}
		}
		data["items"] = local
	}
	return p, nil
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
		for _, field := range []string{"items", "observations", "headcounts"} {
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
		source, _ := p.sources[ref].(map[string]any)
		asOf, hasDate := node["as_of"]
		if source["publisher"] == "FNS" && hasDate {
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
	for _, key := range []string{"state", "reason", "as_of", "years", "sections", "coverage", "warnings", "next_cursor", "next_offset"} {
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
