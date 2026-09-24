package main

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestSSESplitLineChunkingContract(t *testing.T) {
	const alias = "muse_alias"
	aliases := map[string]string{alias: "original_tool"}
	t.Run("long line stays one frame", func(t *testing.T) {
		data := `{"type":"response.output_item.done","item":{"type":"function_call","name":"` + alias + `"},"padding":"` + strings.Repeat("x", 8<<10) + `"}`
		input := "event: response.output_item.done\ndata: " + data + "\n\n"
		var output bytes.Buffer
		if err := streamSSEWithToolNameRestore(&output, strings.NewReader(input), nil, aliases); err != nil {
			t.Fatal(err)
		}
		got := output.String()
		if strings.Count(got, "\n\n") != 1 || strings.Count(got, "data:") != 1 || !strings.Contains(got, `"name":"original_tool"`) {
			t.Fatalf("long line was split into multiple frames or not restored: frames=%d data=%d", strings.Count(got, "\n\n"), strings.Count(got, "data:"))
		}
	})
	t.Run("CRLF and multiple data lines", func(t *testing.T) {
		input := "event: response.output_item.done\r\ndata: {\r\ndata: \"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"" + alias + "\"}}\r\n\r\n"
		var output bytes.Buffer
		if err := streamSSEWithToolNameRestore(&output, strings.NewReader(input), nil, aliases); err != nil {
			t.Fatal(err)
		}
		got := output.String()
		if strings.Count(got, "event:") != 1 || !strings.Contains(got, `"name":"original_tool"`) || !strings.HasSuffix(got, "\r\n\r\n") {
			t.Fatalf("CRLF/multiline event changed incorrectly: %q", got)
		}
	})
	t.Run("EOF data line", func(t *testing.T) {
		input := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"" + alias + "\"}}"
		var output bytes.Buffer
		if err := streamSSEWithToolNameRestore(&output, strings.NewReader(input), nil, aliases); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), alias) || !strings.Contains(output.String(), "original_tool") {
			t.Fatalf("EOF frame was not restored: %q", output.String())
		}
	})
}

type shortWriteOnlyWriter struct{ bytes.Buffer }

func (w shortWriteOnlyWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func TestWriteSSEFrameReturnsShortWrite(t *testing.T) {
	writer := shortWriteOnlyWriter{}
	frame := []byte("data: [DONE]\n\n")
	if err := writeSSEFrame(writer, frame, nil, nil, maxSSEFrameBytes); err != io.ErrShortWrite {
		t.Fatalf("writeSSEFrame error=%v want=%v", err, io.ErrShortWrite)
	}
}
