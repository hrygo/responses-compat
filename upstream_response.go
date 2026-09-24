package main

import (
	"io"
	"mime"
	"net/http"
	"strings"
)

// relayUpstreamResponse writes the downstream response but does not close resp.Body.
// serveResponses retains ownership of the upstream body.
func relayUpstreamResponse(w http.ResponseWriter, resp *http.Response, aliases *toolNameAliases, extraHeaders []string) {
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
		copyResponseHeaders(w.Header(), resp.Header, extraHeaders)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(rewritten)
		return
	}

	copyResponseHeaders(w.Header(), resp.Header, extraHeaders)
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
