package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

const compatibilityLongToolName = "mcp__codex_apps__codex_document_control___execute_document_command"

func compatibilityRequestBodyForTest() []byte {
	return []byte(`{"model":"example-model","store":true,"previous_response_id":"synthetic","input":[{"type":"reasoning","id":"r1","encrypted_content":"opaque"},{"type":"function_call","name":"` + compatibilityLongToolName + `","arguments":"{}"},{"type":"additional_tools","tools":[{"type":"function","name":"` + compatibilityLongToolName + `","parameters":{"type":"object"}}]}],"tools":[{"type":"function","name":"` + compatibilityLongToolName + `","strict":true,"parameters":{"type":"object","properties":{"value":{"$ref":"#/$defs/Text"}},"$defs":{"Text":{"type":"object","properties":{"label":{"type":"string"}}}}}}],"tool_choice":{"type":"function","name":"` + compatibilityLongToolName + `"},"metadata":{"name":"` + compatibilityLongToolName + `"},"custom":{"name":"` + compatibilityLongToolName + `"}}`)
}

func TestPassthroughPreservesBytesAndClientChoices(t *testing.T) {
	body := []byte("{\n  \"model\": \"example-model\", \"store\": true, \"previous_response_id\": \"synthetic\", \"input\": [{\"type\":\"reasoning\",\"id\":\"r1\"}], \"tools\": [{\"type\":\"function\",\"name\":\"f\",\"strict\":true,\"parameters\":{\"$ref\":\"#/$defs/Text\",\"$defs\":{\"Text\":{\"type\":\"string\"}}}}]\n}")
	got, aliases, err := normalizeRequestWithPolicy(body, []string{"example-model"}, passthroughPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) || aliases.hasAliases() {
		t.Fatal("passthrough transformed the request")
	}
}

func TestBuiltInCompatibilityPolicies(t *testing.T) {
	if got, want := musePolicy(), (CompatibilityPolicy{SchemaRefs: "inline", RecursiveRefs: "empty_schema", ToolNameMaxBytes: 64, ReasoningIDs: "drop"}); got != want {
		t.Fatalf("Muse policy=%+v want=%+v", got, want)
	}
	if got, want := passthroughPolicy(), (CompatibilityPolicy{SchemaRefs: "preserve", RecursiveRefs: "reject", ToolNameMaxBytes: 0, ReasoningIDs: "preserve"}); got != want {
		t.Fatalf("passthrough policy=%+v want=%+v", got, want)
	}
}

func TestCompatibilityPoliciesIsolateTransforms(t *testing.T) {
	body := compatibilityRequestBodyForTest()

	t.Run("drop reasoning id only", func(t *testing.T) {
		policy := CompatibilityPolicy{SchemaRefs: "preserve", RecursiveRefs: "reject", ToolNameMaxBytes: 0, ReasoningIDs: "drop"}
		got, aliases, err := normalizeRequestWithPolicy(body, []string{"example-model"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if aliases.hasAliases() {
			t.Fatal("disabled name aliasing created aliases")
		}
		request := decodeObject(t, got)
		input := request["input"].([]any)
		if _, exists := input[0].(map[string]any)["id"]; exists {
			t.Fatal("reasoning id was not dropped")
		}
		if input[1].(map[string]any)["name"] != compatibilityLongToolName || request["tools"].([]any)[0].(map[string]any)["name"] != compatibilityLongToolName {
			t.Fatal("drop-only policy changed tool names")
		}
		parameters := request["tools"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
		if parameters["$ref"] != nil || parameters["properties"].(map[string]any)["value"].(map[string]any)["$ref"] != "#/$defs/Text" {
			t.Fatal("drop-only policy changed schema references")
		}
		if request["store"] != true || request["previous_response_id"] != "synthetic" || request["tools"].([]any)[0].(map[string]any)["strict"] != true {
			t.Fatal("client choices were changed")
		}
	})

	t.Run("alias only", func(t *testing.T) {
		policy := CompatibilityPolicy{SchemaRefs: "preserve", RecursiveRefs: "reject", ToolNameMaxBytes: 16, ReasoningIDs: "preserve"}
		got, aliases, err := normalizeRequestWithPolicy(body, []string{"example-model"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if !aliases.hasAliases() {
			t.Fatal("expected tool aliases")
		}
		alias := aliases.toUpstream[compatibilityLongToolName]
		if len(alias) > 16 {
			t.Fatalf("alias bytes=%d exceeds policy limit", len(alias))
		}
		request := decodeObject(t, got)
		if request["tools"].([]any)[0].(map[string]any)["name"] != alias || request["tool_choice"].(map[string]any)["name"] != alias {
			t.Fatal("tool definition or choice was not aliased")
		}
		input := request["input"].([]any)
		if input[1].(map[string]any)["name"] != alias || input[2].(map[string]any)["tools"].([]any)[0].(map[string]any)["name"] != alias {
			t.Fatal("tool call or additional tool was not aliased")
		}
		if input[0].(map[string]any)["id"] != "r1" {
			t.Fatal("alias-only policy changed reasoning id")
		}
		parameters := request["tools"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
		if parameters["properties"].(map[string]any)["value"].(map[string]any)["$ref"] != "#/$defs/Text" {
			t.Fatal("alias-only policy expanded schema references")
		}
		if request["metadata"].(map[string]any)["name"] != compatibilityLongToolName || request["custom"].(map[string]any)["name"] != compatibilityLongToolName {
			t.Fatal("alias-only policy changed unknown name fields")
		}
		if request["store"] != true || request["previous_response_id"] != "synthetic" || request["tools"].([]any)[0].(map[string]any)["strict"] != true {
			t.Fatal("client choices were changed")
		}
	})

	t.Run("inline only", func(t *testing.T) {
		policy := CompatibilityPolicy{SchemaRefs: "inline", RecursiveRefs: "empty_schema", ToolNameMaxBytes: 0, ReasoningIDs: "preserve"}
		got, aliases, err := normalizeRequestWithPolicy(body, []string{"example-model"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if aliases.hasAliases() {
			t.Fatal("inline-only policy created aliases")
		}
		request := decodeObject(t, got)
		parameters := request["tools"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
		if _, exists := parameters["$defs"]; exists {
			t.Fatal("inline-only policy retained definitions")
		}
		value := parameters["properties"].(map[string]any)["value"].(map[string]any)
		if value["type"] != "object" || value["$ref"] != nil {
			t.Fatalf("local reference not expanded: %#v", value)
		}
		if request["tools"].([]any)[0].(map[string]any)["name"] != compatibilityLongToolName || request["tool_choice"].(map[string]any)["name"] != compatibilityLongToolName {
			t.Fatal("inline-only policy changed tool names")
		}
		if request["input"].([]any)[0].(map[string]any)["id"] != "r1" {
			t.Fatal("inline-only policy changed reasoning id")
		}
		if request["store"] != true || request["previous_response_id"] != "synthetic" || request["tools"].([]any)[0].(map[string]any)["strict"] != true {
			t.Fatal("client choices were changed")
		}
	})
}

func TestNormalizeRequestWithPolicyUsesExactModelAllowlist(t *testing.T) {
	body := []byte(`{"model":"muse-spark-1.3-contributor","input":"ping"}`)
	if _, _, err := normalizeRequestWithPolicy(body, []string{"muse-spark-1.3-contributor"}, passthroughPolicy()); err != nil {
		t.Fatalf("listed model rejected: %v", err)
	}
	if _, _, err := normalizeRequestWithPolicy(body, []string{"muse-spark-1.3"}, passthroughPolicy()); err == nil {
		t.Fatal("model prefix was accepted instead of exact match")
	}
}

func TestNormalizeRequestReturnsOriginalBytesWhenPolicyDoesNotTransform(t *testing.T) {
	body := []byte("{\n  \"model\": \"example-model\", \"input\": \"ping\", \"tools\": [{\"type\":\"function\",\"name\":\"short\",\"parameters\":{\"type\":\"object\"}}]\n}")
	policy := CompatibilityPolicy{SchemaRefs: "inline", RecursiveRefs: "empty_schema", ToolNameMaxBytes: 64, ReasoningIDs: "preserve"}
	got, aliases, err := normalizeRequestWithPolicy(body, []string{"example-model"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if aliases.hasAliases() || !bytes.Equal(got, body) {
		t.Fatal("no-op normalization changed the original bytes")
	}
}

func TestBuildToolNameAliasesRespectsConfiguredThresholds(t *testing.T) {
	for _, limit := range []int{16, 32, 64, 256} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			atLimit := strings.Repeat("s", limit)
			aboveLimit := strings.Repeat("n", limit+1)
			request := map[string]any{"tools": []any{
				map[string]any{"type": "function", "name": atLimit},
				map[string]any{"type": "function", "name": aboveLimit},
			}}
			aliases, err := buildToolNameAliasesWithLimit(request, limit)
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := aliases.toUpstream[atLimit]; exists {
				t.Fatal("name at the byte threshold was aliased")
			}
			if got := aliases.toUpstream[aboveLimit]; got == "" || len(got) > limit {
				t.Fatalf("name above threshold received invalid alias %q", got)
			}
		})
	}
}

func TestBuildToolNameAliasesZeroDisablesAliasing(t *testing.T) {
	request := map[string]any{"tools": []any{map[string]any{"type": "function", "name": compatibilityLongToolName}}}
	aliases, err := buildToolNameAliasesWithLimit(request, 0)
	if err != nil {
		t.Fatal(err)
	}
	if aliases.hasAliases() {
		t.Fatal("zero threshold did not disable aliases")
	}
}

func TestFunctionToolAlias64RetainsLegacyResult(t *testing.T) {
	legacy := "muse_ac78f13219594144fa578918b8f0b80c5f89e610ddc59b81f010f7fa73"
	if got := functionToolAlias(compatibilityLongToolName); got != legacy {
		t.Fatalf("64-byte legacy alias changed: got=%q want=%q", got, legacy)
	}
	request := map[string]any{"tools": []any{map[string]any{"type": "function", "name": compatibilityLongToolName}}}
	aliases, err := buildToolNameAliasesWithLimit(request, 64)
	if err != nil {
		t.Fatal(err)
	}
	if aliases.toUpstream[compatibilityLongToolName] != legacy {
		t.Fatalf("configured 64-byte alias changed: %#v", aliases.toUpstream)
	}
}

func TestNormalizeRequestWithPolicyRecursiveReferenceModes(t *testing.T) {
	body := []byte(`{"model":"example-model","tools":[{"type":"function","name":"tree","parameters":{"$defs":{"Node":{"type":"object","properties":{"child":{"$ref":"#/$defs/Node"}}}},"$ref":"#/$defs/Node"}}]}`)
	emptyPolicy := CompatibilityPolicy{SchemaRefs: "inline", RecursiveRefs: "empty_schema", ToolNameMaxBytes: 0, ReasoningIDs: "preserve"}
	normalized, _, err := normalizeRequestWithPolicy(body, []string{"example-model"}, emptyPolicy)
	if err != nil {
		t.Fatal(err)
	}
	request := decodeObject(t, normalized)
	parameters := request["tools"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
	child := parameters["properties"].(map[string]any)["child"].(map[string]any)
	if len(child) != 0 {
		t.Fatalf("empty_schema recursion policy changed: %#v", child)
	}

	rejectPolicy := emptyPolicy
	rejectPolicy.RecursiveRefs = "reject"
	if _, _, err := normalizeRequestWithPolicy(body, []string{"example-model"}, rejectPolicy); err == nil || normalizationCode(err) != normalizationCodeRecursiveSchemaUnsupported {
		t.Fatalf("recursive reference was not rejected with a fixed code: %v", err)
	}
}
