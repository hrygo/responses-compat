package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelayUpstreamResponseDecisionMatrix(t *testing.T) {
	const originalName = "original_tool_name_for_client"
	const alias = "muse_alias"
	jsonBody := `{"output":[{"type":"function_call","name":"` + alias + `"}]}`
	aliases := &toolNameAliases{toUpstream: map[string]string{originalName: alias}, toClient: map[string]string{alias: originalName}}
	cases := []struct {
		name        string
		status      int
		contentType string
		body        io.ReadCloser
		aliases     *toolNameAliases
		wantStatus  int
		wantBody    string
		wantHeader  string
		wantFlush   bool
	}{
		{"created JSON rewrite", http.StatusCreated, "application/json", io.NopCloser(strings.NewReader(jsonBody)), aliases, http.StatusCreated, originalName, "", false},
		{"vendor JSON rewrite", http.StatusOK, "application/vnd.example+json", io.NopCloser(strings.NewReader(jsonBody)), aliases, http.StatusOK, originalName, "", false},
		{"unsafe media rejected", http.StatusOK, "text/plain", io.NopCloser(strings.NewReader("secret upstream body")), aliases, http.StatusBadGateway, "upstream response media type cannot be safely rewritten", "", false},
		{"plain text passthrough", http.StatusOK, "text/plain", io.NopCloser(strings.NewReader("plain bytes")), nil, http.StatusOK, "plain bytes", "", false},
		{"non-success passthrough", http.StatusTooManyRequests, "text/plain", io.NopCloser(strings.NewReader("rate limited")), aliases, http.StatusTooManyRequests, "rate limited", "2", false},
		{"JSON read failure", http.StatusCreated, "application/json", &failingReadCloser{data: []byte(`{"output":`)}, aliases, http.StatusBadGateway, "could not read upstream response", "", false},
		{"SSE recognized event", http.StatusOK, "text/event-stream", io.NopCloser(strings.NewReader("event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"" + alias + "\"}}\n\n")), aliases, http.StatusOK, originalName, "", true},
		{"unknown SSE passthrough", http.StatusOK, "text/event-stream", io.NopCloser(strings.NewReader(": comment\n\nevent: vendor.notice\ndata: {not-json}\n\n")), nil, http.StatusOK, "vendor.notice", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: tc.body}
			resp.Header.Set("Content-Type", tc.contentType)
			if tc.wantHeader != "" {
				resp.Header.Set("Retry-After", tc.wantHeader)
			}
			recorder := httptest.NewRecorder()
			relayUpstreamResponse(recorder, resp, tc.aliases, nil)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), tc.wantBody) {
				t.Fatalf("body=%q missing %q", recorder.Body.String(), tc.wantBody)
			}
			if tc.wantHeader != "" && recorder.Header().Get("Retry-After") != tc.wantHeader {
				t.Fatalf("Retry-After=%q want=%q", recorder.Header().Get("Retry-After"), tc.wantHeader)
			}
			if tc.wantFlush && !recorder.Flushed {
				t.Fatal("SSE response was not flushed")
			}
			if tc.status == http.StatusCreated && tc.wantStatus == http.StatusBadGateway && recorder.Code != http.StatusBadGateway {
				t.Fatal("invalid JSON must fail before upstream status is committed")
			}
		})
	}
}

func TestRelayUpstreamResponseJSONFailureDoesNotCommitUpstreamStatus(t *testing.T) {
	writer := &statusTrackingWriter{header: make(http.Header)}
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       &failingReadCloser{data: []byte(`{"output":`)},
	}
	relayUpstreamResponse(writer, resp, &toolNameAliases{toUpstream: map[string]string{"original": "alias"}, toClient: map[string]string{"alias": "original"}}, nil)
	if writer.status != http.StatusBadGateway || writer.status == http.StatusCreated {
		t.Fatalf("committed status=%d want=%d body=%q", writer.status, http.StatusBadGateway, writer.body.String())
	}
}

type statusTrackingWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *statusTrackingWriter) Header() http.Header { return w.header }
func (w *statusTrackingWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *statusTrackingWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func TestRelayUpstreamResponseWithoutFlusher(t *testing.T) {
	body := ": keepalive\n\nevent: vendor.notice\ndata: untouched\n\n"
	writer := &statusTrackingWriter{header: make(http.Header)}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	relayUpstreamResponse(writer, resp, nil, nil)
	if writer.status != http.StatusOK || writer.body.String() != body {
		t.Fatalf("status=%d body=%q", writer.status, writer.body.String())
	}
}
