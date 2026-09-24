package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func NewHandler(upstream *url.URL, client *http.Client) http.Handler {
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		serveResponses(w, r, upstream, &clientCopy)
	})
	return mux
}

func serveResponses(w http.ResponseWriter, r *http.Request, upstream *url.URL, client *http.Client) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request size exceeds limit")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "could not read request")
		return
	}
	var envelope struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if envelope.Model != museModel {
		writeJSONError(w, http.StatusBadRequest, "unsupported model")
		return
	}
	payload, aliases, err := normalizeRequestWithToolNames(body)
	if err != nil {
		writeJSONErrorWithCode(w, http.StatusUnprocessableEntity, "invalid Muse Responses request", normalizationCode(err))
		return
	}

	target := *upstream
	target.Path = strings.TrimSuffix(target.Path, "/") + "/responses"
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	out, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "could not create upstream request")
		return
	}
	for _, name := range []string{
		"Accept",
		"Authorization",
		"Originator",
		"Session-Id",
		"Thread-Id",
		"User-Agent",
		"Version",
		"X-Client-Request-Id",
		"X-Codex-Beta-Features",
		"X-Codex-Turn-Metadata",
		"X-Codex-Turn-State",
		"X-Codex-Window-Id",
		"X-Openai-Internal-Codex-Responses-Lite",
		"X-Openai-Subagent",
		"X-Opencode-Session",
	} {
		for _, value := range r.Header.Values(name) {
			out.Header.Add(name, value)
		}
	}
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Accept-Encoding", "identity")

	resp, err := client.Do(out)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "Muse upstream request failed")
		return
	}
	defer resp.Body.Close()
	isSSE := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	isJSON := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json")
	canRewriteSuccess := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	if aliases.hasAliases() && canRewriteSuccess && isJSON && !isSSE {
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseRewriteBytes+1))
		if readErr != nil {
			writeJSONError(w, http.StatusBadGateway, "could not read Muse response")
			return
		}
		if len(responseBody) > maxResponseRewriteBytes {
			writeJSONError(w, http.StatusBadGateway, "Muse response exceeds tool-name rewrite size limit")
			return
		}
		rewritten, _, rewriteErr := restoreToolNamesInJSON(responseBody, aliases.toClient, maxResponseRewriteBytes)
		if rewriteErr != nil {
			writeJSONError(w, http.StatusBadGateway, "Muse response could not be safely rewritten")
			return
		}
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(rewritten)
		return
	}

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if isSSE {
		flusher, canFlush := w.(http.Flusher)
		if canFlush {
			flusher.Flush()
		}
		if aliases.hasAliases() && canRewriteSuccess {
			var eventFlusher http.Flusher
			if canFlush {
				eventFlusher = flusher
			}
			_ = streamSSEWithToolNameRestore(w, resp.Body, eventFlusher, aliases.toClient)
			return
		}
		_, _ = io.CopyBuffer(flushWriter{writer: w, flusher: flusher, enabled: canFlush}, resp.Body, make([]byte, 32<<10))
		return
	}
	_, _ = io.CopyBuffer(w, resp.Body, make([]byte, 32<<10))
}

type flushWriter struct {
	writer  io.Writer
	flusher http.Flusher
	enabled bool
}

func (w flushWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if w.enabled {
		w.flusher.Flush()
	}
	return n, err
}

func copyResponseHeaders(dst, src http.Header) {
	for _, name := range []string{"Cache-Control", "Content-Type", "Retry-After", "X-Openai-Request-Id", "X-Request-Id"} {
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSONErrorWithCode(w, status, message, "")
}

func writeJSONErrorWithCode(w http.ResponseWriter, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	detail := map[string]string{
		"type":    "invalid_request_error",
		"message": message,
	}
	if code != "" {
		detail["code"] = code
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": detail})
}
