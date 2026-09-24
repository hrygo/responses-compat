package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRestoreToolNamesInJSONResponse(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := "mcp__codex_apps__codex_document_control___execute_document_command"
	body := []byte(`{"status":"completed","output":[{"type":"function_call","name":"` + alias + `","call_id":"call-1","arguments":"{}"},{"type":"message","content":[{"type":"output_text","text":"` + alias + `"}]}]}`)
	got, changed, err := restoreToolNamesInJSON(body, map[string]string{alias: original}, maxResponseRewriteBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("function-call name was not restored")
	}
	var response map[string]any
	if err := json.Unmarshal(got, &response); err != nil {
		t.Fatal(err)
	}
	output := response["output"].([]any)
	call := output[0].(map[string]any)
	if call["name"] != original {
		t.Fatalf("restored name=%v", call["name"])
	}
	message := output[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if message["text"] != alias {
		t.Fatalf("ordinary message text was modified: %v", message["text"])
	}
}

func TestRestoreToolNamesInJSONPreservesUnchangedBody(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	for _, body := range [][]byte{
		[]byte(`{"status":"completed","output":[{"type":"message","content":[]}]}`),
	} {
		got, changed, err := restoreToolNamesInJSON(body, map[string]string{alias: "original"}, maxResponseRewriteBytes)
		if err != nil || changed || !bytes.Equal(got, body) {
			t.Fatalf("body=%q changed=%v err=%v got=%q", body, changed, err, got)
		}
	}
}

func TestRestoreToolNamesInResponseMetadata(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := "mcp__codex_apps__codex_document_control___execute_document_command"
	body := []byte(`{"type":"response.created","response":{"tools":[{"type":"function","name":"` + alias + `"}]}}`)
	got, changed, err := restoreToolNamesInJSON(body, map[string]string{alias: original}, maxResponseRewriteBytes)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !bytes.Contains(got, []byte(`"name":"`+original+`"`)) || bytes.Contains(got, []byte(`"name":"`+alias+`"`)) {
		t.Fatalf("response tool metadata name was not restored: %s", got)
	}
}

func expandedAliasResponse(alias string, count int) []byte {
	output := make([]any, count)
	for index := range output {
		output[index] = map[string]any{
			"type": "function_call",
			"name": alias,
		}
	}
	body, err := json.Marshal(map[string]any{"output": output})
	if err != nil {
		panic(err)
	}
	return body
}

func TestRestoreToolNamesRejectsExpandedJSONOverRewriteLimit(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := strings.Repeat("x", 256)
	body := expandedAliasResponse(alias, 2)

	got, changed, err := restoreToolNamesInJSON(body, map[string]string{alias: original}, 256)
	if err == nil {
		t.Fatal("expected expanded response to exceed the rewrite limit")
	}
	if changed || len(got) != 0 {
		t.Fatalf("oversized response returned partial output: changed=%v bytes=%d", changed, len(got))
	}
}

func TestEncodedJSONQuotedStringSizeMatchesEncoder(t *testing.T) {
	values := []string{
		"plain text",
		"quotes: \" and slash: / and backslash: \\",
		"HTML: <>&",
		"controls: \x00\b\f\n\r\t",
		"unicode: café 😀 \u2028 \u2029",
	}
	for _, value := range values {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
		want := int64(len(bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))))
		if got := encodedJSONQuotedStringSize(value); got != want {
			t.Errorf("encoded size for %q = %d, want %d", value, got, want)
		}
	}
}

func TestRestoreToolNamesUsesExactEncodedOutputLimit(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := "quoted: \" slash: / backslash: \\ newline: \n unicode: \u2028"
	body := expandedAliasResponse(alias, 2)
	aliases := map[string]string{alias: original}

	expected, changed, err := restoreToolNamesInJSON(body, aliases, maxResponseRewriteBytes)
	if err != nil || !changed {
		t.Fatalf("could not establish expected output: changed=%v err=%v", changed, err)
	}
	maxOutput := len(expected)
	got, changed, err := restoreToolNamesInJSON(body, aliases, maxOutput)
	if err != nil || !changed || len(got) != maxOutput {
		t.Fatalf("exact limit rejected: changed=%v bytes=%d limit=%d err=%v", changed, len(got), maxOutput, err)
	}
	if _, changed, err := restoreToolNamesInJSON(body, aliases, maxOutput-1); err == nil || changed {
		t.Fatalf("limit below exact output length was accepted: changed=%v err=%v", changed, err)
	}
}

func TestRestoreToolNamesRejectsMalformedJSONInsteadOfLeakingAlias(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	body := []byte(`{"output":[{"type":"function_call","name":"` + alias)
	got, changed, err := restoreToolNamesInJSON(body, map[string]string{alias: "long_function_name"}, maxResponseRewriteBytes)
	if err == nil || changed || len(got) != 0 {
		t.Fatalf("malformed response was forwarded: changed=%v bytes=%d err=%v", changed, len(got), err)
	}
}

func TestRestoreIgnoresNonToolMetadataName(t *testing.T) {
	body := []byte(`{"metadata":{"name":"alias"},"output":[{"type":"function_call","name":"alias","arguments":"{\"name\":\"alias\"}"}]}`)
	got, changed, err := restoreToolNamesInJSON(body, map[string]string{"alias": "original"}, maxResponseRewriteBytes)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !bytes.Contains(got, []byte(`"metadata":{"name":"alias"}`)) {
		t.Fatal("metadata name was changed")
	}
	if !bytes.Contains(got, []byte(`"arguments":"{\"name\":\"alias\"}"`)) {
		t.Fatal("function arguments were changed")
	}
	if !bytes.Contains(got, []byte(`"name":"original"`)) {
		t.Fatal("function-call name was not restored")
	}
}

func TestRestoreUsesOnlyRecognizedResponsePaths(t *testing.T) {
	alias := "alias"
	original := "long_function_name"
	body := []byte(`{"type":"response.completed","response":{"output":[{"type":"function_call","name":"alias","arguments":"{\"name\":\"alias\"}"}],"tools":[{"type":"namespace","name":"alias","tools":[{"type":"function","name":"alias"}]}],"metadata":{"name":"alias"}}}`)
	got, changed, err := restoreToolNamesInJSON(body, map[string]string{alias: original}, maxResponseRewriteBytes)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	response := decoded["response"].(map[string]any)
	call := response["output"].([]any)[0].(map[string]any)
	if call["name"] != original || call["arguments"] != `{"name":"alias"}` {
		t.Fatalf("function-call output mismatch: %#v", call)
	}
	namespace := response["tools"].([]any)[0].(map[string]any)
	if namespace["name"] != alias {
		t.Fatalf("namespace name changed: %#v", namespace)
	}
	function := namespace["tools"].([]any)[0].(map[string]any)
	if function["name"] != original {
		t.Fatalf("function definition name was not restored: %#v", function)
	}
	metadata := response["metadata"].(map[string]any)
	if metadata["name"] != alias {
		t.Fatalf("metadata name changed: %#v", metadata)
	}

}

func TestRestoreRootFunctionCallName(t *testing.T) {
	body := []byte(`{"type":"function_call","name":"alias","arguments":"{\"name\":\"alias\"}"}`)
	got, changed, err := restoreToolNamesInJSON(body, map[string]string{"alias": "long_function_name"}, maxResponseRewriteBytes)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var response map[string]any
	if err := json.Unmarshal(got, &response); err != nil {
		t.Fatal(err)
	}
	if response["name"] != "long_function_name" || response["arguments"] != `{"name":"alias"}` {
		t.Fatalf("root function-call mismatch: %#v", response)
	}
}
