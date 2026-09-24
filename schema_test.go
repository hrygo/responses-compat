package main

import (
	"encoding/json"
	"testing"
)

func decodeObject(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExpandSchemaNodeEnforcesExpansionBudget(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"first":  map[string]any{"type": "string"},
			"second": map[string]any{"type": "string"},
		},
	}
	_, err := expandSchemaNode(schema, schema, make(map[string]bool), 0, &schemaBudget{limit: 16})
	if err == nil {
		t.Fatal("expanded schema over budget was accepted")
	}
}
