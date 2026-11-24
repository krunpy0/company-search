package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEventSourceOptInControlsSchemaAndExecution(t *testing.T) {
	args := json.RawMessage(`{"id":"inn:7707049388","dataset":"taxoffence"}`)
	executor := Executor{Events: &Events{}}
	if string(executor.Schemas()) != string(Schemas()) {
		t.Fatal("disabled sources changed the default tool schemas")
	}
	if err := executor.Execute(context.Background(), "", "get_company_events", args).Error; err == nil || err.Code != "unknown_tool" {
		t.Fatalf("disabled tool executed: %#v", err)
	}
	executor.Events.Support = true
	var catalog map[string]schema
	if err := json.Unmarshal(executor.Schemas(), &catalog); err != nil {
		t.Fatal(err)
	}
	datasets := catalog["get_company_events"].Properties["dataset"].Enum
	if len(datasets) != 1 || datasets[0] != "rsmppp" {
		t.Fatalf("unexpected advertised datasets: %v", datasets)
	}
	if err := executor.Execute(context.Background(), "", "get_company_events", args).Error; err == nil || err.Code != "invalid_arguments" {
		t.Fatalf("disabled dataset accepted: %#v", err)
	}
}
