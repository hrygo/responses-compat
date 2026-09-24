package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestHandlerPreservesUpstreamErrorStatusAndBody(t *testing.T) {
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
			resp, err := front.Client().Post(front.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"muse-spark-1.3-contributor","input":"ping"}`))
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
