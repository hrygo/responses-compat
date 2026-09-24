package main

import (
	"bytes"
	"encoding/json"
	"strings"
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

func TestNormalizeRequestDirectCycle(t *testing.T) {
	in := []byte(`{"model":"muse-spark-1.3-contributor","input":"ping","tools":[{"type":"function","name":"f","parameters":{"$defs":{"Node":{"type":"object","properties":{"child":{"$ref":"#/$defs/Node"}}}},"type":"object","properties":{"root":{"$ref":"#/$defs/Node"}}}}]}`)
	out, err := NormalizeRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(`"$ref"`)) || bytes.Contains(out, []byte(`"$defs"`)) {
		t.Fatalf("unresolved schema: %s", out)
	}
	got := decodeObject(t, out)
	if got["model"] != "muse-spark-1.3-contributor" || got["input"] != "ping" {
		t.Fatalf("non-schema fields changed: %#v", got)
	}
	tools := got["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["name"] != "f" {
		t.Fatalf("tool name changed: %#v", tool)
	}
	params := tool["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	root := props["root"].(map[string]any)
	child := root["properties"].(map[string]any)["child"].(map[string]any)
	if len(child) != 0 {
		t.Fatalf("cycle back-edge not made permissive: %#v", child)
	}
}

func TestNormalizeRequestIndirectCyclePreservesShape(t *testing.T) {
	in := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"tree","parameters":{"$defs":{"A":{"type":"object","properties":{"b":{"$ref":"#/$defs/B"}}},"B":{"type":"object","properties":{"a":{"$ref":"#/$defs/A"},"label":{"type":"string"}}}},"$ref":"#/$defs/A"}}]}`)
	out, err := NormalizeRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(`"$ref"`)) || bytes.Contains(out, []byte(`"$defs"`)) {
		t.Fatalf("unresolved schema: %s", out)
	}
	if !bytes.Contains(out, []byte(`"label"`)) || !bytes.Contains(out, []byte(`"type":"string"`)) {
		t.Fatalf("non-cyclic schema shape lost: %s", out)
	}
}

func TestNormalizeRequestEscapedJSONPointer(t *testing.T) {
	in := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"escaped","parameters":{"$defs":{"A/B":{"type":"string"}},"properties":{"value":{"$ref":"#/$defs/A~1B"}}}}]}`)
	out, err := NormalizeRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"value":{"type":"string"}`)) {
		t.Fatalf("escaped pointer did not resolve: %s", out)
	}
}

func TestNormalizeRequestRFC6901ArrayAndURIFragmentReferences(t *testing.T) {
	in := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"pointer","parameters":{"$defs":{"A/B":{"type":"string"},"A~B":{"type":"number"},"A B":{"anyOf":[{"type":"boolean"}]}},"type":"object","properties":{"slash":{"$ref":"#/$defs/A~1B"},"tilde":{"$ref":"#/$defs/A~0B"},"array":{"$ref":"#/$defs/A%20B/anyOf/0"}}}}]}`)
	out, err := NormalizeRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(`"$ref"`)) || bytes.Contains(out, []byte(`"$defs"`)) {
		t.Fatalf("unresolved pointer reference: %s", out)
	}
	got := decodeObject(t, out)
	params := got["tools"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	for name, want := range map[string]string{"slash": "string", "tilde": "number", "array": "boolean"} {
		if props[name].(map[string]any)["type"] != want {
			t.Fatalf("%s reference resolved incorrectly: %#v", name, props[name])
		}
	}
}

func TestNormalizeRequestDepthBoundary(t *testing.T) {
	normalizeAtDepth := func(nesting int) error {
		var schema any = map[string]any{"type": "string"}
		for i := 0; i < nesting; i++ {
			schema = map[string]any{"not": schema}
		}
		parameters, err := json.Marshal(schema)
		if err != nil {
			return err
		}
		request := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"deep","parameters":` + string(parameters) + `}]}`)
		_, err = NormalizeRequest(request)
		return err
	}
	if err := normalizeAtDepth(maxSchemaDepth - 1); err != nil {
		t.Fatalf("schema at configured depth boundary rejected: %v", err)
	}
	if err := normalizeAtDepth(maxSchemaDepth); err == nil {
		t.Fatal("schema over configured depth boundary was accepted")
	}
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

func TestNormalizeRequestNestedCodexToolShapes(t *testing.T) {
	in := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"namespace","name":"mcp__codex_app","tools":[{"type":"function","name":"request_environment_input","parameters":{"$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"$ref":"#/$defs/Node"}}]}],"input":[{"type":"additional_tools","tools":[{"name":"functions","tools":[{"type":"function","name":"recursive","parameters":{"$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"$ref":"#/$defs/Node"}}]}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"keep"}]}]}`)
	out, err := NormalizeRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(`"$ref"`)) {
		t.Fatalf("recursive ref remains: %s", out)
	}
	got := decodeObject(t, out)
	if got["model"] != "muse-spark-1.3-contributor" {
		t.Fatal("model changed")
	}
	input := got["input"].([]any)
	if len(input) != 2 || input[1].(map[string]any)["type"] != "message" {
		t.Fatalf("input envelope changed: %#v", input)
	}
	if !bytes.Contains(out, []byte(`"request_environment_input"`)) || !bytes.Contains(out, []byte(`"recursive"`)) {
		t.Fatalf("tool removed: %s", out)
	}
}

func TestNormalizeRequestRejectsMissingAndExternalRefs(t *testing.T) {
	cases := map[string]string{
		"missing":  `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"f","parameters":{"$ref":"#/$defs/missing"}}]}`,
		"external": `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"f","parameters":{"$ref":"https://example.test/schema.json"}}]}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeRequest([]byte(in)); err == nil {
				t.Fatal("expected explicit schema error")
			}
		})
	}
}

func TestNormalizeRequestRejectsWrongModelAndInvalidJSON(t *testing.T) {
	if _, err := NormalizeRequest([]byte(`{"model":"other","tools":[]}`)); err == nil {
		t.Fatal("expected model rejection")
	}
	if _, err := NormalizeRequest([]byte(`{"model":`)); err == nil {
		t.Fatal("expected JSON rejection")
	}
}

func TestNormalizeRequestEnforcesSizeAndDepth(t *testing.T) {
	big := `{"model":"muse-spark-1.3-contributor","input":"` + strings.Repeat("x", maxRequestBytes) + `"}`
	if _, err := NormalizeRequest([]byte(big)); err == nil {
		t.Fatal("expected size rejection")
	}
}

func TestNormalizeRequestAliasesOverlongFunctionToolNames(t *testing.T) {
	longName := "mcp__codex_apps__codex_document_control___execute_document_command"
	if len(longName) <= maxFunctionToolNameLength {
		t.Fatalf("fixture name length=%d; want over limit", len(longName))
	}
	request := map[string]any{
		"model": museModel,
		"tools": []any{map[string]any{
			"type":       "function",
			"name":       longName,
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
		}},
		"tool_choice": map[string]any{"type": "function", "name": longName},
		"input":       []any{map[string]any{"type": "function_call", "call_id": "call-1", "name": longName, "arguments": "{}"}},
	}
	input, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := NormalizeRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeObject(t, normalized)
	toolName := got["tools"].([]any)[0].(map[string]any)["name"].(string)
	choiceName := got["tool_choice"].(map[string]any)["name"].(string)
	inputName := got["input"].([]any)[0].(map[string]any)["name"].(string)
	if toolName == longName || len(toolName) > maxFunctionToolNameLength {
		t.Fatalf("overlong tool name was not aliased: len=%d", len(toolName))
	}
	if choiceName != toolName || inputName != toolName {
		t.Fatalf("related names diverged: tool=%q choice=%q input=%q", toolName, choiceName, inputName)
	}
}
