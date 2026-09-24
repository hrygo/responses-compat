package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
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

func TestRewriteSSEFrameRestoresToolNamesAndPreservesOtherEvents(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := "mcp__codex_apps__codex_document_control___execute_document_command"
	frame := []byte("event: response.output_item.done\r\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"" + alias + "\",\"call_id\":\"call-1\"}}\r\n\r\n")
	got, changed, err := rewriteSSEFrame(frame, map[string]string{alias: original}, maxSSEFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("SSE function-call name was not restored")
	}
	if !bytes.Contains(got, []byte(`"name":"`+original+`"`)) || !bytes.Contains(got, []byte("event: response.output_item.done\r\n")) {
		t.Fatalf("rewritten event lost its function name or event header: %q", got)
	}
	done := []byte("data: [DONE]\n\n")
	if got, changed, err := rewriteSSEFrame(done, map[string]string{alias: original}, maxSSEFrameBytes); err != nil || changed || !bytes.Equal(got, done) {
		t.Fatalf("[DONE] event changed: %q changed=%v", got, changed)
	}
}

func TestRewriteSSEFrameHandlesMultipleDataLines(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := "long_function_name"
	frame := []byte("event: response.completed\ndata: {\"response\":\ndata: {\"output\":[{\"type\":\"function_call\",\"name\":\"" + alias + "\"}]}}\n\n")
	got, changed, err := rewriteSSEFrame(frame, map[string]string{alias: original}, maxSSEFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !bytes.Contains(got, []byte(`"name":"`+original+`"`)) || !strings.HasSuffix(string(got), "\n\n") {
		t.Fatalf("multi-line SSE event was not rewritten: changed=%v body=%q", changed, got)
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

func TestRewriteSSEFrameRestoresNameOnToolArgumentEvents(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := "mcp__codex_apps__codex_document_control___execute_document_command"
	frame := []byte("event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"name\":\"" + alias + "\",\"arguments\":\"{}\"}\n\n")
	got, changed, err := rewriteSSEFrame(frame, map[string]string{alias: original}, maxSSEFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !bytes.Contains(got, []byte(`"name":"`+original+`"`)) || bytes.Contains(got, []byte(`"name":"`+alias+`"`)) {
		t.Fatalf("tool argument event name was not restored: changed=%v frame=%q", changed, got)
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

func TestRewriteSSEFrameRejectsExpandedEventOverRewriteLimit(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := strings.Repeat("x", 256)
	body := expandedAliasResponse(alias, 2)
	frame := append([]byte("event: response.completed\ndata: "), body...)
	frame = append(frame, []byte("\n\n")...)

	got, changed, err := rewriteSSEFrame(frame, map[string]string{alias: original}, 256)
	if err == nil {
		t.Fatal("expected expanded SSE event to exceed the rewrite limit")
	}
	if changed || len(got) != 0 {
		t.Fatalf("oversized event returned partial output: changed=%v bytes=%d", changed, len(got))
	}
}

func TestWriteSSEFrameEmitsErrorInsteadOfOversizedRewrite(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	original := strings.Repeat("x", 256)
	body := expandedAliasResponse(alias, 2)
	frame := append([]byte("event: response.completed\ndata: "), body...)
	frame = append(frame, []byte("\n\n")...)
	recorder := httptest.NewRecorder()

	err := writeSSEFrame(recorder, frame, recorder, map[string]string{alias: original}, 256)
	if err == nil {
		t.Fatal("expected an SSE rewrite limit error")
	}
	if !strings.Contains(recorder.Body.String(), "event: response.failed") || strings.Contains(recorder.Body.String(), alias) {
		t.Fatalf("limit failure was not reported without leaking the alias: %q", recorder.Body.String())
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

func TestRewriteSSEFrameRejectsMalformedJSONInsteadOfLeakingAlias(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	frame := []byte("event: response.completed\ndata: {\"name\":\"" + alias + "\n\n")
	got, changed, err := rewriteSSEFrame(frame, map[string]string{alias: "long_function_name"}, maxSSEFrameBytes)
	if err == nil || changed || len(got) != 0 {
		t.Fatalf("malformed event was forwarded: changed=%v bytes=%d err=%v", changed, len(got), err)
	}
}

func TestWriteSSEFrameEmitsErrorForMalformedJSON(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	frame := []byte("event: response.completed\ndata: {\"name\":\"" + alias + "\n\n")
	recorder := httptest.NewRecorder()

	err := writeSSEFrame(recorder, frame, recorder, map[string]string{alias: "long_function_name"}, maxSSEFrameBytes)
	if err == nil {
		t.Fatal("expected a malformed-event rewrite error")
	}
	if !strings.Contains(recorder.Body.String(), "event: response.failed") || strings.Contains(recorder.Body.String(), alias) {
		t.Fatalf("malformed-event failure leaked an alias: %q", recorder.Body.String())
	}
}

func TestStreamSSEEmitsTerminalResponseFailedOnMalformedEvent(t *testing.T) {
	alias := "muse_tool_0123456789012345678901234567890123456789012345678901234567"
	input := "event: response.completed\ndata: {\"name\":\"" + alias + "\n\n" + "data: [DONE]\n\n"
	recorder := httptest.NewRecorder()

	err := streamSSEWithToolNameRestore(recorder, strings.NewReader(input), recorder, map[string]string{alias: "long_function_name"})
	if err == nil {
		t.Fatal("expected malformed-event rewrite failure")
	}
	got := recorder.Body.String()
	if !strings.Contains(got, "event: response.failed\n") || !strings.Contains(got, "\"type\":\"response.failed\"") {
		t.Fatalf("stream did not emit a terminal Responses failure: %q", got)
	}
	if strings.Contains(got, alias) || strings.Contains(got, "[DONE]") {
		t.Fatalf("failed stream leaked an alias or propagated success terminator: %q", got)
	}
}
