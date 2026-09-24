package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	body := []byte(`{"type":"response.completed","response":` + string(expandedAliasResponse(alias, 2)) + `}`)
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
	body := []byte(`{"type":"response.completed","response":` + string(expandedAliasResponse(alias, 2)) + `}`)
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

func TestSSEBudgetIncludesEnvelope(t *testing.T) {
	payload := []byte(`{"type":"response.output_item.done","item":{"type":"function_call","name":"a"}}`)
	aliases := map[string]string{"a": strings.Repeat("x", 256)}
	rewritten, changed, err := restoreToolNamesInSSEJSON(payload, aliases, 4096, "response.output_item.done")
	if err != nil || !changed {
		t.Fatalf("could not establish rewritten SSE JSON: changed=%v err=%v", changed, err)
	}
	frame := []byte("event: response.output_item.done\ndata: " + string(payload) + "\n\n")
	_, _, err = rewriteSSEFrame(frame, aliases, len(rewritten))
	if !errors.Is(err, errResponseRewriteLimit) {
		t.Fatalf("expected full-frame limit, got %v", err)
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

func TestUnknownSSEEventRemainsUnchanged(t *testing.T) {
	frame := []byte("event: response.unknown\nid: evt-1\n: keep this comment\ndata: {\"type\":\"response.unknown\",\"response\":{\"output\":[{\"type\":\"function_call\",\"name\":\"alias\"}]}}\n\n")
	got, changed, err := rewriteSSEFrame(frame, map[string]string{"alias": "original"}, maxSSEFrameBytes)
	if err != nil || changed || !bytes.Equal(got, frame) {
		t.Fatalf("unknown event changed: changed=%v err=%v frame=%q", changed, err, got)
	}
}

func TestSSEFrameBudgetIncludesPreservedEnvelope(t *testing.T) {
	payload := `{"type":"response.output_item.done","item":{"type":"function_call","name":"a"}}`
	aliases := map[string]string{"a": strings.Repeat("x", 256)}
	for _, ending := range []string{"\n", "\r\n"} {
		frame := []byte("event: response.output_item.done" + ending + "id: evt-1" + ending + ": comment" + ending + "data: " + payload + ending + ending)
		expected, changed, err := rewriteSSEFrame(frame, aliases, maxSSEFrameBytes)
		if err != nil || !changed {
			t.Fatalf("could not build rewritten frame: changed=%v err=%v", changed, err)
		}
		exact, changed, err := rewriteSSEFrame(frame, aliases, len(expected))
		if err != nil || !changed || len(exact) != len(expected) {
			t.Fatalf("exact full-frame limit rejected: changed=%v bytes=%d limit=%d err=%v", changed, len(exact), len(expected), err)
		}
		if _, changed, err := rewriteSSEFrame(frame, aliases, len(expected)-1); !errors.Is(err, errResponseRewriteLimit) || changed {
			t.Fatalf("one-byte-over frame limit accepted: changed=%v err=%v", changed, err)
		}
	}
}

func TestRewriteSSEFramePreservesEOFDataLine(t *testing.T) {
	alias := "alias"
	original := "long_function_name"
	frame := []byte("event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"alias\"}}")
	got, changed, err := rewriteSSEFrame(frame, map[string]string{alias: original}, maxSSEFrameBytes)
	if err != nil || !changed || !bytes.Contains(got, []byte(`"name":"`+original+`"`)) || bytes.HasSuffix(got, []byte("\n")) {
		t.Fatalf("EOF frame was not preserved: changed=%v err=%v frame=%q", changed, err, got)
	}
}

func TestRewriteSSEFrameWithNoAliasesReturnsFrameUnchanged(t *testing.T) {
	frame := []byte("event: response.output_item.done\r\ndata: {\"type\":\"response.output_item.done\"}\r\n\r\n")
	got, changed, err := rewriteSSEFrame(frame, nil, 1)
	if err != nil || changed || !bytes.Equal(got, frame) {
		t.Fatalf("frame changed without a tool mapping: changed=%v err=%v frame=%q", changed, err, got)
	}
}

func TestRewriteSSEFrameRecognizesDocumentedEventPaths(t *testing.T) {
	alias := "alias"
	original := "long_function_name"
	events := []string{
		"response.output_item.added",
		"response.output_item.done",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.created",
		"response.in_progress",
		"response.completed",
		"response.incomplete",
		"response.failed",
	}
	for _, event := range events {
		t.Run(event, func(t *testing.T) {
			var payload string
			switch {
			case strings.HasPrefix(event, "response.output_item."):
				payload = `{"type":"` + event + `","item":{"type":"function_call","name":"alias"}}`
			case strings.HasPrefix(event, "response.function_call_arguments."):
				payload = `{"type":"` + event + `","name":"alias","arguments":"{}"}`
			default:
				payload = `{"type":"` + event + `","response":{"output":[{"type":"function_call","name":"alias"}],"tools":[{"type":"namespace","name":"alias","tools":[{"type":"function","name":"alias"}]}]}}`
			}
			frame := []byte("event: " + event + "\ndata: " + payload + "\n\n")
			got, changed, err := rewriteSSEFrame(frame, map[string]string{alias: original}, maxSSEFrameBytes)
			if err != nil || !changed || !bytes.Contains(got, []byte(`"name":"`+original+`"`)) {
				t.Fatalf("event path was not restored: changed=%v err=%v frame=%q", changed, err, got)
			}
		})
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

func TestStreamSSERewritesEOFDataFrame(t *testing.T) {
	input := `event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","item":{"type":"function_call","name":"alias"}}`
	recorder := httptest.NewRecorder()
	if err := streamSSEWithToolNameRestore(recorder, strings.NewReader(input), recorder, map[string]string{"alias": "long_function_name"}); err != nil {
		t.Fatal(err)
	}
	got := recorder.Body.Bytes()
	if !bytes.Contains(got, []byte(`"name":"long_function_name"`)) || bytes.HasSuffix(got, []byte("\n")) {
		t.Fatalf("EOF data frame was not rewritten without adding a terminator: %q", got)
	}
}
