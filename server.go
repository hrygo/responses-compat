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
	relayUpstreamResponse(w, resp, aliases, config.ExtraResponseHeaders)
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
