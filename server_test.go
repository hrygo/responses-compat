package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerNormalizesAndForwardsMuseRequest(t *testing.T) {
	gotRequest := make(chan *http.Request, 1)
	gotBody := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequest <- r
		body, _ := io.ReadAll(r.Body)
		gotBody <- string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message"}]}`)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL + "/v1")
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	body := `{"model":"muse-spark-1.3-contributor","input":"ping","tools":[{"type":"function","name":"f","parameters":{"$defs":{"N":{"type":"object","properties":{"next":{"$ref":"#/$defs/N"}}}},"$ref":"#/$defs/N"}}]}`
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("x-opencode-session", "codex:test-session")
	req.Header.Set("User-Agent", "Codex Desktop test")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	upReq := <-gotRequest
	if upReq.URL.Path != "/v1/responses" {
		t.Fatalf("path=%s", upReq.URL.Path)
	}
	if upReq.Header.Get("Authorization") != "Bearer test-secret" || upReq.Header.Get("x-opencode-session") != "codex:test-session" {
		t.Fatalf("end-to-end headers lost: %#v", upReq.Header)
	}
	if upReq.Header.Get("User-Agent") != "Codex Desktop test" {
		t.Fatal("user agent lost")
	}
	forwarded := <-gotBody
	if strings.Contains(forwarded, `"$ref"`) || strings.Contains(forwarded, `"$defs"`) || !strings.Contains(forwarded, `"name":"f"`) {
		t.Fatalf("unexpected normalized body: %s", forwarded)
	}
}

func TestHandlerRemovesReasoningItemIDsAndPreservesEncryptedPayload(t *testing.T) {
	forwardedBody := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
		}
		forwardedBody <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"completed","output":[]}`)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	body := `{"model":"muse-spark-1.3-contributor","input":[{"type":"reasoning","id":"rs_expired","encrypted_content":"opaque-reasoning-data","summary":[{"type":"summary_text","text":"preserve this summary"}]},{"type":"function_call_output","call_id":"call-1","output":"MUSE_TOOL_E2E_OK"}]}`
	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	got := decodeObject(t, <-forwardedBody)
	input := got["input"].([]any)
	reasoning := input[0].(map[string]any)
	if _, exists := reasoning["id"]; exists {
		t.Fatalf("unstable reasoning item id was forwarded: %#v", reasoning)
	}
	if reasoning["encrypted_content"] != "opaque-reasoning-data" {
		t.Fatalf("encrypted reasoning payload changed: %#v", reasoning)
	}
	summary := reasoning["summary"].([]any)[0].(map[string]any)
	if summary["text"] != "preserve this summary" {
		t.Fatalf("reasoning summary changed: %#v", summary)
	}
	toolOutput := input[1].(map[string]any)
	if toolOutput["call_id"] != "call-1" || toolOutput["output"] != "MUSE_TOOL_E2E_OK" {
		t.Fatalf("tool output changed: %#v", toolOutput)
	}
}

func TestHandlerPreservesUpstreamErrorStatusAndBody(t *testing.T) {
	longName := "mcp__codex_apps__codex_document_control___execute_document_command"
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"upstream rejected request"}}`)
			}))
			defer upstream.Close()
			base, _ := url.Parse(upstream.URL)
			front := httptest.NewServer(NewHandler(base, upstream.Client()))
			defer front.Close()
			requestBody := `{"model":"` + museModel + `","input":"ping","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}]}`
			resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(requestBody))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			got, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != status || !strings.Contains(string(got), "upstream rejected request") {
				t.Fatalf("status=%d body=%s", resp.StatusCode, got)
			}
		})
	}
}

func TestHandlerPreservesUpstream200ErrorWithoutRewriting(t *testing.T) {
	const errorBody = `{"error":{"message":"provider returned an application error"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, errorBody)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"muse-spark-1.3-contributor","input":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(got) != errorBody {
		t.Fatalf("status=%d body=%s", resp.StatusCode, got)
	}
}

func TestHandlerStreamsSSEErrorEventUnchanged(t *testing.T) {
	const first = "event: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	const second = "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"upstream failed\"}}\n\n"
	firstFlushed := make(chan struct{})
	continueStream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, first)
		flusher.Flush()
		close(firstFlushed)
		<-continueStream
		_, _ = io.WriteString(w, second)
		flusher.Flush()
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/responses", strings.NewReader(`{"model":"muse-spark-1.3-contributor","input":"ping","stream":true}`))
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "event: response.created\n" {
		t.Fatalf("first SSE line=%q err=%v", line, err)
	}
	select {
	case <-firstFlushed:
	default:
		t.Fatal("upstream did not flush the first event")
	}
	close(continueStream)
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	got := line + string(remaining)
	if resp.StatusCode != http.StatusOK || got != first+second {
		t.Fatalf("status=%d stream=%q", resp.StatusCode, got)
	}
}

func TestHandlerFlushesSSEHeadersBeforeFirstEvent(t *testing.T) {
	upstreamHeadersSent := make(chan struct{})
	allowFirstEvent := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(allowFirstEvent) }) }

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(upstreamHeadersSent)
		<-allowFirstEvent
		_, _ = io.WriteString(w, "event: response.created\ndata: {}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()
	defer release()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	request, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/responses", strings.NewReader(`{"model":"muse-spark-1.3-contributor","input":"ping","stream":true}`))
	responseCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		response, err := front.Client().Do(request)
		if err != nil {
			errCh <- err
			return
		}
		responseCh <- response
	}()

	select {
	case <-upstreamHeadersSent:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream did not flush response headers")
	}
	var response *http.Response
	select {
	case response = <-responseCh:
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(time.Second):
		release()
		select {
		case lateResponse := <-responseCh:
			lateResponse.Body.Close()
		case <-errCh:
		case <-time.After(5 * time.Second):
		}
		t.Fatal("client did not receive SSE headers before the first event")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		release()
		t.Fatalf("status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	release()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "event: response.created\n" {
		t.Fatalf("first SSE line=%q err=%v", line, err)
	}
}

func TestHandlerRejectsNonMuseWithoutCallingUpstream(t *testing.T) {
	var called atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()
	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"opencode-go-glm53-flash","input":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || called.Load() {
		t.Fatalf("status=%d upstream_called=%v", resp.StatusCode, called.Load())
	}
}

func TestHandlerRejectsWrongPathAndMethod(t *testing.T) {
	var called atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/v1/responses", http.StatusMethodNotAllowed},
		{http.MethodPost, "/v1/chat/completions", http.StatusNotFound},
	} {
		req, _ := http.NewRequest(tc.method, front.URL+tc.path, strings.NewReader(`{}`))
		resp, err := front.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s %s status=%d want=%d", tc.method, tc.path, resp.StatusCode, tc.status)
		}
	}
	if called.Load() {
		t.Fatal("unexpected upstream request")
	}
}

func TestHandlerHealthzIsLocalAndNoContent(t *testing.T) {
	var called atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	resp, err := front.Client().Get(front.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || called.Load() {
		t.Fatalf("status=%d upstream_called=%v", resp.StatusCode, called.Load())
	}
}

func TestNewHandlerWithNilUpstreamFailsClosed(t *testing.T) {
	handler := NewHandler(nil, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"`+museModel+`","input":"ping"}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("nil upstream unexpectedly selected a default route: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerDoesNotFollowUpstreamRedirect(t *testing.T) {
	var redirectTargetCalled atomic.Bool
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectTargetCalled.Store(true)
	}))
	defer redirectTarget.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, httptest.NewRequest(http.MethodGet, "/", nil), redirectTarget.URL, http.StatusFound)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"muse-spark-1.3-contributor","input":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || redirectTargetCalled.Load() {
		t.Fatalf("status=%d redirect_target_called=%v", resp.StatusCode, redirectTargetCalled.Load())
	}
}

func TestHandlerStreamsBeforeUpstreamCompletesAndPropagatesCancel(t *testing.T) {
	firstChunk := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "event: response.created\ndata: {}\n\n")
		flusher.Flush()
		close(firstChunk)
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+"/v1/responses", strings.NewReader(`{"model":"muse-spark-1.3-contributor","input":"ping","stream":true}`))
	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := front.Client().Do(req)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()
	var resp *http.Response
	select {
	case resp = <-respCh:
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("response headers not streamed")
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "event: response.created\n" {
		cancel()
		resp.Body.Close()
		t.Fatalf("first SSE line=%q err=%v", line, err)
	}
	select {
	case <-firstChunk:
	default:
		cancel()
		resp.Body.Close()
		t.Fatal("upstream did not emit first chunk")
	}
	cancel()
	resp.Body.Close()
	select {
	case <-upstreamCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("client cancellation did not reach upstream")
	}
}

func TestHandlerAliasesLongToolNamesAndRestoresJSONFunctionCall(t *testing.T) {
	longName := "mcp__codex_apps__codex_document_control___execute_document_command"
	alias := functionToolAlias(longName)
	upstreamBodyCh := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamBodyCh <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"function_call","name":"`+alias+`","call_id":"call-1","arguments":"{}"}]}`)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL + "/v1")
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	requestBody := `{"model":"` + museModel + `","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}],"tool_choice":{"type":"function","name":"` + longName + `"}}`
	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, responseBody)
	}
	upstreamBody := <-upstreamBodyCh
	if strings.Contains(upstreamBody, longName) || !strings.Contains(upstreamBody, alias) {
		t.Fatalf("upstream tool name was not shortened: contains_original=%v contains_alias=%v", strings.Contains(upstreamBody, longName), strings.Contains(upstreamBody, alias))
	}
	if !strings.Contains(string(responseBody), `"name":"`+longName+`"`) || strings.Contains(string(responseBody), `"name":"`+alias+`"`) {
		t.Fatalf("client response name was not restored: %s", responseBody)
	}
}

func TestHandlerRestoresLongToolNamesInSSEEvents(t *testing.T) {
	longName := "mcp__codex_apps__codex_document_control___execute_document_command"
	alias := functionToolAlias(longName)
	upstreamBodyCh := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamBodyCh <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\""+alias+"\",\"call_id\":\"call-1\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"name\":\""+alias+"\",\"call_id\":\"call-1\",\"arguments\":\"{}\"}]}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL + "/v1")
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	requestBody := `{"model":"` + museModel + `","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}],"stream":true}`
	req, err := http.NewRequest(http.MethodPost, front.URL+"/v1/responses", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	upstreamBody := <-upstreamBodyCh
	if resp.StatusCode != http.StatusOK || !strings.Contains(upstreamBody, alias) || strings.Contains(upstreamBody, longName) {
		t.Fatalf("status=%d upstream_short_name=%v", resp.StatusCode, strings.Contains(upstreamBody, alias) && !strings.Contains(upstreamBody, longName))
	}
	if !strings.Contains(string(responseBody), `"name":"`+longName+`"`) || strings.Contains(string(responseBody), `"name":"`+alias+`"`) || !strings.Contains(string(responseBody), "data: [DONE]") {
		t.Fatalf("SSE names or terminator were not preserved: %s", responseBody)
	}
}

func TestHandlerRejectsExpandedJSONResponseOverRewriteLimit(t *testing.T) {
	longName := strings.Repeat("x", 1<<20)
	alias := functionToolAlias(longName)
	responseBody := expandedAliasResponse(alias, maxResponseRewriteBytes/(1<<20)+1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responseBody)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL + "/v1")
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	requestBody := `{"model":"` + museModel + `","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}]}`
	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadGateway || strings.Contains(string(got), alias) || strings.Contains(string(got), longName) {
		t.Fatalf("status=%d alias_leaked=%v response_bytes=%d", resp.StatusCode, strings.Contains(string(got), alias), len(got))
	}
}

func TestHandlerEmitsSSEErrorForExpandedEventOverRewriteLimit(t *testing.T) {
	longName := strings.Repeat("x", 1<<20)
	alias := functionToolAlias(longName)
	responseBody := expandedAliasResponse(alias, maxSSEFrameBytes/(1<<20)+1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":")
		_, _ = w.Write(responseBody)
		_, _ = io.WriteString(w, "}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL + "/v1")
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	requestBody := `{"model":"` + museModel + `","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}],"stream":true}`
	req, err := http.NewRequest(http.MethodPost, front.URL+"/v1/responses", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(got), "event: response.failed") || !strings.Contains(string(got), "\"type\":\"response.failed\"") || !strings.Contains(string(got), "resp_responses_compat_error_") || !strings.Contains(string(got), "upstream response could not be safely rewritten") || strings.Contains(string(got), alias) || strings.Contains(string(got), "[DONE]") {
		t.Fatalf("status=%d alias_leaked=%v response_bytes=%d", resp.StatusCode, strings.Contains(string(got), alias), len(got))
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTripper roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

type failingReadCloser struct {
	data []byte
	done bool
}

func (body *failingReadCloser) Read(dst []byte) (int, error) {
	if body.done {
		return 0, io.EOF
	}
	body.done = true
	return copy(dst, body.data), io.ErrUnexpectedEOF
}

func (body *failingReadCloser) Close() error { return nil }

func TestHandlerRejectsPartialJSONWhenUpstreamReadFails(t *testing.T) {
	longName := "mcp__codex_apps__codex_document_control___execute_document_command"
	alias := functionToolAlias(longName)
	upstream, _ := url.Parse("https://opencode.invalid/v1")
	client := &http.Client{Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		partialBody := []byte(`{"output":[{"type":"function_call","name":"` + alias)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body:       &failingReadCloser{data: partialBody},
		}, nil
	})}
	front := httptest.NewServer(NewHandler(upstream, client))
	defer front.Close()

	requestBody := `{"model":"` + museModel + `","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}]}`
	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadGateway || strings.Contains(string(got), alias) {
		t.Fatalf("status=%d alias_leaked=%v response_bytes=%d", resp.StatusCode, strings.Contains(string(got), alias), len(got))
	}
}

func TestUnknownSuccessMediaWithAliasesFailsClosed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Request-Id", "must-not-commit")
		_, _ = io.WriteString(w, "synthetic upstream text")
	}))
	defer upstream.Close()
	base, err := url.Parse(upstream.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	longName := strings.Repeat("x", maxFunctionToolNameLength+1)
	body := `{"model":"` + museModel + `","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}]}`
	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d body=%q", resp.StatusCode, got)
	}
	if strings.Contains(string(got), "synthetic upstream text") || strings.Contains(string(got), "muse_") {
		t.Fatalf("upstream body or internal alias leaked: %q", got)
	}
	if header := resp.Header.Get("X-Request-Id"); header != "" {
		t.Fatalf("upstream response headers were committed before rejecting media type: %q", header)
	}
}

func TestConfiguredHandlerFiltersConnectionNominatedHeaders(t *testing.T) {
	type observed struct {
		traceID         string
		privateHop      string
		connection      string
		authorization   string
		contentEncoding string
	}
	got := make(chan observed, 1)
	config := defaultConfig()
	config.UpstreamBaseURL = "https://upstream.example/v1"
	config.Profile = "passthrough"
	config.Policy = passthroughPolicy()
	config.ExtraRequestHeaders = []string{"X-Trace-Id", "X-Private-Hop"}
	config.ExtraResponseHeaders = []string{"X-Upstream-Trace", "X-Private-Response"}
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		header.Set("Content-Length", "10")
		header.Set("Content-Encoding", "gzip")
		header.Set("X-Upstream-Trace", "visible")
		header.Set("Connection", "X-Private-Response")
		header.Set("X-Private-Response", "must-not-leak")
		got <- observed{
			traceID:         request.Header.Get("X-Trace-Id"),
			privateHop:      request.Header.Get("X-Private-Hop"),
			connection:      request.Header.Get("Connection"),
			authorization:   request.Header.Get("Authorization"),
			contentEncoding: request.Header.Get("Content-Encoding"),
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`)), Request: request}, nil
	})}
	handler, err := NewConfiguredHandler(config, client)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"`+museModel+`","input":"ping"}`))
	request.Header.Set("Connection", "X-Private-Hop, Authorization")
	request.Header.Set("X-Trace-Id", "trace-1")
	request.Header.Set("X-Private-Hop", "must-not-forward")
	request.Header.Set("Authorization", "Bearer synthetic-secret")
	request.Header.Set("Content-Encoding", "gzip")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case observation := <-got:
		if observation.traceID != "trace-1" || observation.privateHop != "" || observation.connection != "" || observation.authorization != "" || observation.contentEncoding != "" {
			t.Fatalf("request headers were not safely filtered: %+v", observation)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request was not observed")
	}
	if got := recorder.Header().Get("X-Upstream-Trace"); got != "visible" {
		t.Fatalf("configured response header=%q", got)
	}
	if got := recorder.Header().Get("X-Private-Response"); got != "" {
		t.Fatalf("Connection-nominated response header leaked: %q", got)
	}
	if got := recorder.Header().Get("Content-Length"); got != "" {
		t.Fatalf("upstream Content-Length leaked: %q", got)
	}
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("upstream Content-Encoding leaked: %q", got)
	}
}

func TestUnknownSuccessMediaWithoutAliasesPassesThrough(t *testing.T) {
	const body = "synthetic upstream text"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()
	base, err := url.Parse(upstream.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"`+museModel+`","input":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(got) != body {
		t.Fatalf("status=%d body=%q", resp.StatusCode, got)
	}
}

func TestNon2xxUnknownMediaWithAliasesPassesThrough(t *testing.T) {
	const body = "synthetic upstream rejection"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()
	base, err := url.Parse(upstream.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	longName := strings.Repeat("x", maxFunctionToolNameLength+1)
	requestBody := `{"model":"` + museModel + `","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}]}`
	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnprocessableEntity || string(got) != body {
		t.Fatalf("status=%d body=%q", resp.StatusCode, got)
	}
}

func TestVendorJSONMediaRewritesToolNameAliases(t *testing.T) {
	longName := strings.Repeat("x", maxFunctionToolNameLength+1)
	alias := functionToolAlias(longName)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.example.response+json; charset=utf-8")
		_, _ = io.WriteString(w, `{"output":[{"type":"function_call","name":"`+alias+`"}]}`)
	}))
	defer upstream.Close()
	base, err := url.Parse(upstream.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	requestBody := `{"model":"` + museModel + `","tools":[{"type":"function","name":"` + longName + `","parameters":{"type":"object"}}]}`
	resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(got), longName) || strings.Contains(string(got), alias) {
		t.Fatalf("status=%d body=%s", resp.StatusCode, got)
	}
}

func TestHandlerAcceptsSupportedRefSiblingsAndClassifiesConflict(t *testing.T) {
	var upstreamCalls atomic.Int32
	forwarded := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
		}
		forwarded <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed"}`)
	}))
	defer upstream.Close()
	base, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(NewHandler(base, upstream.Client()))
	defer front.Close()

	valid := `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"example","parameters":{"properties":{"value":{"$ref":"#/$defs/Text","type":"string","description":"synthetic annotation"}},"$defs":{"Text":{"type":"string"}}}}]}`
	response, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("supported siblings status=%d", response.StatusCode)
	}
	var gotForwarded string
	select {
	case gotForwarded = <-forwarded:
	case <-time.After(5 * time.Second):
		t.Fatal("valid request did not reach upstream")
	}
	if strings.Contains(gotForwarded, `"$ref"`) || !strings.Contains(gotForwarded, `"description":"synthetic annotation"`) {
		t.Fatalf("unexpected forwarded request: %s", gotForwarded)
	}

	conflict := `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"example","parameters":{"$ref":"#/$defs/Text","type":"number","$defs":{"Text":{"type":"string"}}}}]}`
	response, err = front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(conflict))
	if err != nil {
		t.Fatal(err)
	}
	errorBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(errorBody), `"code":"schema_ref_sibling_unsupported"`) {
		t.Fatalf("status=%d body=%s", response.StatusCode, errorBody)
	}
	if strings.Contains(string(errorBody), "Text") || strings.Contains(string(errorBody), "number") || strings.Contains(string(errorBody), "$defs") {
		t.Fatalf("internal schema details leaked: %s", errorBody)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls=%d want=1", got)
	}
}

func TestHandlerRequestAdmissionContract(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"malformed", `{"model":`, http.StatusBadRequest, ""},
		{"array", `[]`, http.StatusBadRequest, ""},
		{"null", `null`, http.StatusBadRequest, ""},
		{"wrong model type", `{"model":17}`, http.StatusBadRequest, ""},
		{"model checked before schema", `{"model":"not-allowed","tools":[{"parameters":17}]}`, http.StatusBadRequest, ""},
		{"case-sensitive envelope", `{"Model":"muse-spark-1.3-contributor"}`, http.StatusUnprocessableEntity, "invalid_request"},
		{"trailing object", `{"model":"muse-spark-1.3-contributor"} {}`, http.StatusBadRequest, ""},
		{"invalid parameters", `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"f","parameters":17}]}`, http.StatusUnprocessableEntity, "invalid_request"},
		{"schema sibling conflict", `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"f","parameters":{"$ref":"#/$defs/Text","type":"number","$defs":{"Text":{"type":"string"}}}}]}`, http.StatusUnprocessableEntity, "schema_ref_sibling_unsupported"},
		{"oversized", strings.Repeat(" ", maxRequestBytes+1), http.StatusRequestEntityTooLarge, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})}
			handler := newTestConfiguredHandler(t, client)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.body))
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.status, recorder.Body.String())
			}
			if tc.code != "" && !strings.Contains(recorder.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("body=%s missing code %q", recorder.Body.String(), tc.code)
			}
			if got := calls.Load(); got != 0 {
				t.Fatalf("upstream calls=%d want=0 (name=%s)", got, tc.name)
			}
		})
	}
}

func TestHandlerPreservesSuccessfulCreatedStatus(t *testing.T) {
	const originalName = "mcp__codex_apps__codex_document_control___execute_document_command"
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"output":[{"type":"function_call","name":"` + functionToolAlias(originalName) + `"}]}`)),
		}, nil
	})}
	handler := newTestConfiguredHandler(t, client)
	body := `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"` + originalName + `","parameters":{"type":"object"}}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"name":"`+originalName+`"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type closeTrackingReadCloser struct {
	reader *strings.Reader
	closes int
}

func (r *closeTrackingReadCloser) Read(p []byte) (int, error) { return r.reader.Read(p) }
func (r *closeTrackingReadCloser) Close() error               { r.closes++; return nil }

func TestHandlerClosesUpstreamBodyAcrossResponseBranches(t *testing.T) {
	const originalName = "mcp__codex_apps__codex_document_control___execute_document_command"
	cases := []struct {
		name, contentType, body string
		status                  int
		wantStatus              int
	}{
		{"normal rewrite", "application/json", `{"output":[{"type":"function_call","name":"` + functionToolAlias(originalName) + `"}]}`, http.StatusOK, http.StatusOK},
		{"rejected media type", "text/plain", "secret upstream body", http.StatusOK, http.StatusBadGateway},
		{"rewrite failure", "application/json", "not json", http.StatusOK, http.StatusBadGateway},
		{"non-success passthrough", "text/plain", "upstream error", http.StatusTooManyRequests, http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracked := &closeTrackingReadCloser{reader: strings.NewReader(tc.body)}
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}}, Body: tracked}, nil
			})}
			handler := newTestConfiguredHandler(t, client)
			body := `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"` + originalName + `","parameters":{"type":"object"}}]}`
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tracked.closes != 1 {
				t.Fatalf("upstream Body.Close calls=%d want=1", tracked.closes)
			}
		})
	}
}

func newTestConfiguredHandler(t *testing.T, client *http.Client) http.Handler {
	t.Helper()
	config := defaultConfig()
	config.UpstreamBaseURL = "https://upstream.example/v1"
	handler, err := NewConfiguredHandler(config, client)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
