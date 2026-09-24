package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

var forwardedRequestHeaders = []string{
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
}

var forwardedResponseHeaders = []string{
	"Cache-Control",
	"Content-Type",
	"Retry-After",
	"X-Openai-Request-Id",
	"X-Request-Id",
}

// NewHandler preserves the original fixed Muse handler API for existing callers.
func NewHandler(upstream *url.URL, client *http.Client) http.Handler {
	config := defaultConfig()
	if upstream == nil {
		config.UpstreamBaseURL = ""
	} else {
		config.UpstreamBaseURL = upstream.String()
	}
	handler, err := NewConfiguredHandler(config, client)
	if err == nil {
		return handler
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusServiceUnavailable, "adapter configuration is invalid")
	})
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusInternalServerError, "adapter configuration is invalid")
	})
	return mux
}

func NewConfiguredHandler(config RuntimeConfig, client *http.Client) (http.Handler, error) {
	if err := validateRuntimeConfig(config); err != nil {
		return nil, err
	}
	upstream, err := parseUpstreamBaseURL(config.UpstreamBaseURL)
	if err != nil {
		return nil, err
	}
	requestHeaders, err := normalizeExtraHeaderNames(config.ExtraRequestHeaders, "request")
	if err != nil {
		return nil, err
	}
	responseHeaders, err := normalizeExtraHeaderNames(config.ExtraResponseHeaders, "response")
	if err != nil {
		return nil, err
	}

	config = cloneRuntimeConfig(config)
	config.ExtraRequestHeaders = requestHeaders
	config.ExtraResponseHeaders = responseHeaders
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	upstreamCopy := *upstream

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		serveResponses(w, r, &upstreamCopy, &clientCopy, config)
	})
	return mux, nil
}

func serveResponses(w http.ResponseWriter, r *http.Request, upstream *url.URL, client *http.Client, config RuntimeConfig) {
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
	if !modelAllowed(envelope.Model, config.Models) {
		writeJSONError(w, http.StatusBadRequest, "unsupported model")
		return
	}
	payload, aliases, err := normalizeRequestWithPolicy(body, config.Models, config.Policy)
	if err != nil {
		writeJSONErrorWithCode(w, http.StatusUnprocessableEntity, "invalid Responses request", normalizationCode(err))
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
	copyRequestHeaders(out.Header, r.Header, config.ExtraRequestHeaders)
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Accept-Encoding", "identity")

	resp, err := client.Do(out)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream request failed")
		return
	}
	defer resp.Body.Close()
	mediaType := parsedMediaType(resp.Header.Get("Content-Type"))
	isSSE := mediaType == "text/event-stream"
	isJSON := isJSONMediaType(mediaType)
	canRewriteSuccess := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	if aliases.hasAliases() && canRewriteSuccess && !isJSON && !isSSE {
		writeJSONError(w, http.StatusBadGateway, "upstream response media type cannot be safely rewritten")
		return
	}
	if aliases.hasAliases() && canRewriteSuccess && isJSON {
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseRewriteBytes+1))
		if readErr != nil {
			writeJSONError(w, http.StatusBadGateway, "could not read upstream response")
			return
		}
		if len(responseBody) > maxResponseRewriteBytes {
			writeJSONError(w, http.StatusBadGateway, "upstream response exceeds tool-name rewrite size limit")
			return
		}
		rewritten, _, rewriteErr := restoreToolNamesInJSON(responseBody, aliases.toClient, maxResponseRewriteBytes)
		if rewriteErr != nil {
			writeJSONError(w, http.StatusBadGateway, "upstream response could not be safely rewritten")
			return
		}
		copyResponseHeaders(w.Header(), resp.Header, config.ExtraResponseHeaders)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(rewritten)
		return
	}

	copyResponseHeaders(w.Header(), resp.Header, config.ExtraResponseHeaders)
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

func copyRequestHeaders(dst, src http.Header, extra []string) {
	nominated := connectionHeaderTokens(src)
	for _, name := range append(append([]string(nil), forwardedRequestHeaders...), extra...) {
		lower := strings.ToLower(name)
		if isHopByHopHeader(lower) || isRequestControlledHeader(lower) || nominated[lower] {
			continue
		}
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header, extra []string) {
	nominated := connectionHeaderTokens(src)
	for _, name := range append(append([]string(nil), forwardedResponseHeaders...), extra...) {
		lower := strings.ToLower(name)
		if isHopByHopHeader(lower) || isResponseControlledHeader(lower) || nominated[lower] {
			continue
		}
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}

func connectionHeaderTokens(header http.Header) map[string]bool {
	tokens := make(map[string]bool)
	for _, headerName := range []string{"Connection", "Proxy-Connection"} {
		for _, value := range header.Values(headerName) {
			for _, token := range strings.Split(value, ",") {
				if token = strings.TrimSpace(token); token != "" {
					tokens[strings.ToLower(token)] = true
				}
			}
		}
	}
	return tokens
}

func isRequestControlledHeader(lower string) bool {
	switch lower {
	case "host", "content-length", "content-type", "content-encoding", "accept-encoding":
		return true
	default:
		return false
	}
}

func isResponseControlledHeader(lower string) bool {
	return lower == "content-length" || lower == "content-encoding"
}

func parsedMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(mediaType)
}

func isJSONMediaType(mediaType string) bool {
	if mediaType == "application/json" {
		return true
	}
	if !strings.HasPrefix(mediaType, "application/") {
		return false
	}
	subtype := strings.TrimPrefix(mediaType, "application/")
	plus := strings.LastIndex(subtype, "+")
	return plus > 0 && subtype[plus:] == "+json"
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
