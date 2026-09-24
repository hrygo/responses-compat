package main

import (
	"errors"
	"net/http"
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

func copyRequestHeaders(dst, src http.Header, extra []string) {
	allowed := append(append([]string(nil), forwardedRequestHeaders...), extra...)
	copyAllowedHeaders(dst, src, allowed, isRequestControlledHeader)
}

func copyResponseHeaders(dst, src http.Header, extra []string) {
	allowed := append(append([]string(nil), forwardedResponseHeaders...), extra...)
	copyAllowedHeaders(dst, src, allowed, isResponseControlledHeader)
}

func copyAllowedHeaders(dst, src http.Header, allowed []string, controlled func(string) bool) {
	nominated := connectionHeaderTokens(src)
	for _, name := range allowed {
		lower := strings.ToLower(name)
		if isHopByHopHeader(lower) || controlled(lower) || nominated[lower] {
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

func normalizeExtraHeaderNames(names []string, direction string) ([]string, error) {
	if direction != "request" && direction != "response" {
		return nil, errors.New("invalid header direction")
	}
	seen := make(map[string]struct{}, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		if !validHeaderName(name) {
			return nil, errors.New("invalid extra header name")
		}
		canonical := http.CanonicalHeaderKey(name)
		lower := strings.ToLower(canonical)
		if isHopByHopHeader(lower) || isControlledHeader(lower) || isAlreadyForwardedHeader(lower, direction) {
			return nil, errors.New("extra header is controlled or hop-by-hop")
		}
		if _, exists := seen[lower]; exists {
			continue
		}
		seen[lower] = struct{}{}
		result = append(result, canonical)
	}
	return result, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isHTTPTokenByte(name[i]) {
			return false
		}
	}
	return true
}

func isHTTPTokenByte(value byte) bool {
	if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' {
		return true
	}
	switch value {
	case 33, 35, 36, 37, 38, 39, 42, 43, 45, 46, 94, 95, 96, 124, 126:
		return true
	default:
		return false
	}
}

func isHopByHopHeader(lower string) bool {
	switch lower {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func isControlledHeader(lower string) bool {
	return isRequestControlledHeader(lower) || isResponseControlledHeader(lower)
}

func isAlreadyForwardedHeader(lower, direction string) bool {
	var names []string
	if direction == "request" {
		names = forwardedRequestHeaders
	} else {
		names = forwardedResponseHeaders
	}
	for _, name := range names {
		if strings.EqualFold(name, lower) {
			return true
		}
	}
	return false
}
