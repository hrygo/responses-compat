package main

import "testing"

func TestBuildToolNameAliasesCoversAdditionalToolsAndLeavesShortNames(t *testing.T) {
	longName := "mcp__codex_apps__codex_document_control___execute_document_command"
	shortName := "probe_echo"
	request := map[string]any{
		"tools": []any{map[string]any{
			"type":  "namespace",
			"name":  "mcp__codex_apps",
			"tools": []any{map[string]any{"type": "function", "name": shortName}},
		}},
		"input": []any{
			map[string]any{
				"type": "additional_tools",
				"tools": []any{map[string]any{
					"type":  "namespace",
					"name":  "additional",
					"tools": []any{map[string]any{"type": "function", "name": longName}},
				}},
			},
			map[string]any{"type": "function_call", "name": longName, "call_id": "call-1"},
		},
	}
	aliases, err := buildToolNameAliases(request)
	if err != nil {
		t.Fatal(err)
	}
	if !aliases.hasAliases() {
		t.Fatal("expected an alias for the long additional tool")
	}
	alias := aliases.toUpstream[longName]
	if len(alias) > maxFunctionToolNameLength || alias == longName {
		t.Fatalf("invalid alias length=%d", len(alias))
	}
	aliases.rewriteRequest(request)
	additional := request["input"].([]any)[0].(map[string]any)["tools"].([]any)[0].(map[string]any)
	gotLong := additional["tools"].([]any)[0].(map[string]any)["name"]
	gotInputCall := request["input"].([]any)[1].(map[string]any)["name"]
	gotShort := request["tools"].([]any)[0].(map[string]any)["tools"].([]any)[0].(map[string]any)["name"]
	if gotLong != alias || gotInputCall != alias || gotShort != shortName {
		t.Fatalf("unexpected rewritten names: additional=%v input_call=%v short=%v", gotLong, gotInputCall, gotShort)
	}
}

func TestFunctionToolAliasIsStableAndCollisionSafe(t *testing.T) {
	longName := "mcp__codex_apps__codex_document_control___execute_document_command"
	first := functionToolAlias(longName)
	second := functionToolAlias(longName)
	if first != second || len(first) > maxFunctionToolNameLength {
		t.Fatalf("alias is not stable and within limit: first=%q second=%q", first, second)
	}
	request := map[string]any{"tools": []any{
		map[string]any{"type": "function", "name": longName},
		map[string]any{"type": "function", "name": first},
	}}
	if _, err := buildToolNameAliases(request); err == nil {
		t.Fatal("alias collision with an existing function name was accepted")
	}
}
