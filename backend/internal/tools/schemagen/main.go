// Command schemagen generates tool argument schemas. Run it from backend.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

const output = "internal/tools/schemas.json"

// Stage 9 tool names map to OpenAPI operationId values.
var operationIDs = map[string]string{
	"search_companies":       "searchCompanies",
	"get_company_profile":    "getCompany",
	"get_company_financials": "getCompanyFinancials",
	"get_company_taxes":      "getCompanyTaxes",
	"get_company_sources":    "getCompanySources",
}

type parameter struct {
	Name        string
	Required    bool
	Description string
	Schema      map[string]any
}

type operation struct {
	ID         string `yaml:"operationId"`
	Summary    string
	Parameters []parameter
}

type argumentSchema struct {
	Description          string                    `json:"description,omitempty"`
	Type                 string                    `json:"type"`
	Properties           map[string]map[string]any `json:"properties"`
	Required             []string                  `json:"required"`
	AdditionalProperties bool                      `json:"additionalProperties"`
}

func main() {
	log.SetFlags(0)
	check := flag.Bool("check", false, "compare the generated schemas with schemas.json")
	flag.Parse()
	data, err := generate()
	if err != nil {
		log.Fatal(err)
	}
	if *check {
		current, err := os.ReadFile(output)
		if err != nil {
			log.Fatal(err)
		}
		if !bytes.Equal(bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n")), data) {
			log.Fatal("schemas.json is out of date. Run go run ./internal/tools/schemagen from backend.")
		}
		return
	}
	if err := os.WriteFile(output, data, 0644); err != nil {
		log.Fatal(err)
	}
}

func generate() ([]byte, error) {
	api, err := os.ReadFile("openapi.yaml")
	if err != nil {
		return nil, err
	}
	var spec struct {
		Paths map[string]struct{ Get operation }
	}
	if err := yaml.Unmarshal(api, &spec); err != nil {
		return nil, err
	}
	operations := make(map[string]operation)
	for _, path := range spec.Paths {
		if path.Get.ID != "" {
			if _, exists := operations[path.Get.ID]; exists {
				return nil, fmt.Errorf("duplicate operationId %s", path.Get.ID)
			}
			operations[path.Get.ID] = path.Get
		}
	}

	doc, err := os.ReadFile("../docs/ARCHITECTURE.md")
	if err != nil {
		return nil, err
	}
	_, stage9, found := strings.Cut(string(doc), "\n## F. ")
	if !found {
		return nil, fmt.Errorf("missing stage 9 contract section in ARCHITECTURE.md")
	}
	stage9, _, _ = strings.Cut(stage9, "\n## ")
	schemas := make(map[string]argumentSchema)
	for _, line := range strings.Split(stage9, "\n") {
		line, ok := strings.CutPrefix(line, "| `")
		if !ok {
			continue
		}
		name, rest, _ := strings.Cut(line, "` | ")
		arguments, _, _ := strings.Cut(rest, " | ")
		operation, exists := operations[operationIDs[name]]
		if !exists {
			return nil, fmt.Errorf("%s: missing mapping to an OpenAPI operation", name)
		}
		if _, exists := schemas[name]; exists {
			return nil, fmt.Errorf("%s: duplicate stage 9 contract", name)
		}
		schema, err := argumentProperties(arguments, operation.Parameters)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		schema.Description = operation.Summary
		schemas[name] = schema
	}
	if len(schemas) != len(operationIDs) {
		return nil, fmt.Errorf("expected %d stage 9 contracts, found %d", len(operationIDs), len(schemas))
	}
	data, err := json.MarshalIndent(schemas, "", "  ")
	return append(data, '\n'), err
}

func argumentProperties(arguments string, params []parameter) (argumentSchema, error) {
	result := argumentSchema{Type: "object", Properties: make(map[string]map[string]any), Required: []string{}}
	// The caller supplies user_id separately from the tool arguments.
	args, ok := strings.CutPrefix(arguments, "`user_id: string`, ")
	if !ok {
		return result, fmt.Errorf("expected a caller-supplied user_id")
	}
	byName := make(map[string]parameter, len(params))
	for _, p := range params {
		if _, exists := byName[p.Name]; exists {
			return result, fmt.Errorf("duplicate OpenAPI parameter %s", p.Name)
		}
		byName[p.Name] = p
	}
	for _, arg := range strings.Split(args, ", ") {
		field, kind, _ := strings.Cut(strings.Trim(arg, "`"), ": ")
		name, optional := strings.CutSuffix(field, "?")
		p, exists := byName[name]
		if !exists || p.Required == optional || !matchesType(kind, p.Schema) {
			return result, fmt.Errorf("stage 9 argument %q does not match OpenAPI", arg)
		}
		delete(byName, name)
		if p.Description != "" {
			p.Schema["description"] = p.Description
		}
		result.Properties[name] = p.Schema
		if p.Required {
			result.Required = append(result.Required, name)
		}
	}
	if len(byName) != 0 {
		return result, fmt.Errorf("OpenAPI contains parameters missing from stage 9")
	}
	return result, nil
}

func matchesType(kind string, schema map[string]any) bool {
	if values, ok := schema["enum"].([]any); ok {
		if schema["type"] != "string" {
			return false
		}
		want := strings.Split(kind, " \\| ")
		got := make([]string, 0, len(values))
		for _, value := range values {
			name, ok := value.(string)
			if !ok {
				return false
			}
			got = append(got, name)
		}
		slices.Sort(want)
		slices.Sort(got)
		return slices.Equal(want, got)
	}
	switch kind {
	case "int":
		return schema["type"] == "integer"
	case "date":
		return schema["type"] == "string" && schema["format"] == "date"
	default:
		return kind == "string" && schema["type"] == "string" && schema["format"] == nil
	}
}
