package main

import (
	"net/http"
	"testing"
)

func TestHeaderPolicyContract(t *testing.T) {
	for _, direction := range []string{"request", "response"} {
		t.Run(direction, func(t *testing.T) {
			for _, name := range []string{"Content-Type", "Content-Length", "Content-Encoding", "Host", "Accept-Encoding", "Connection"} {
				if _, err := normalizeExtraHeaderNames([]string{name}, direction); err == nil {
					t.Errorf("%s accepted controlled header %q", direction, name)
				}
			}
			got, err := normalizeExtraHeaderNames([]string{"X-Trace-Id", "x-trace-id"}, direction)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0] != "X-Trace-Id" {
				t.Fatalf("normalized names=%v", got)
			}
			for _, name := range []string{"", "Bad Header", "Bad\rHeader", "Bad\nHeader"} {
				if _, err := normalizeExtraHeaderNames([]string{name}, direction); err == nil {
					t.Errorf("%s accepted invalid header name %q", direction, name)
				}
			}
			defaults := forwardedRequestHeaders
			if direction == "response" {
				defaults = forwardedResponseHeaders
			}
			for _, name := range defaults {
				if _, err := normalizeExtraHeaderNames([]string{name}, direction); err == nil {
					t.Errorf("%s accepted default header %q as extra", direction, name)
				}
			}
		})
	}
}

func TestHeaderCopyPreservesValues(t *testing.T) {
	src := make(http.Header)
	src.Set("Accept", "application/json")
	src.Set("Content-Type", "application/json")
	src.Set("Content-Encoding", "gzip")
	src.Add("X-Trace-Id", "one")
	src.Add("X-Trace-Id", "two")
	src.Add("X-Allowed", "kept")
	src.Add("X-Connection", "hidden")
	src.Set("Connection", "X-Connection, X-Request-Controlled")
	src.Set("X-Request-Controlled", "hidden")
	src.Set("Proxy-Connection", "X-Proxy-Controlled")
	src.Set("X-Proxy-Controlled", "hidden")
	src.Set("Content-Length", "999")
	dst := make(http.Header)
	copyResponseHeaders(dst, src, []string{"X-Trace-Id", "X-Allowed", "X-Connection", "X-Request-Controlled", "X-Proxy-Controlled", "Content-Encoding", "Content-Length"})
	if dst.Get("Content-Type") != "application/json" || dst.Get("Content-Encoding") != "" || dst.Get("Content-Length") != "" {
		t.Fatalf("response control policy changed: %v", dst)
	}
	values := dst.Values("X-Trace-Id")
	if len(values) != 2 || values[0] != "one" || values[1] != "two" {
		t.Fatalf("lost multivalue header: %v", values)
	}
	if dst.Get("X-Allowed") != "kept" {
		t.Fatalf("extra header missing: %v", dst)
	}
	for _, name := range []string{"X-Connection", "X-Request-Controlled", "X-Proxy-Controlled"} {
		if dst.Get(name) != "" {
			t.Errorf("Connection-nominated header %q was copied: %v", name, dst)
		}
	}

	requestDst := make(http.Header)
	copyRequestHeaders(requestDst, src, []string{"X-Trace-Id", "X-Request-Controlled", "X-Proxy-Controlled", "Content-Type", "Content-Length"})
	if requestDst.Get("Accept") != "application/json" || requestDst.Get("Content-Type") != "" || requestDst.Get("Content-Length") != "" {
		t.Fatalf("request allowlist/control policy changed: %v", requestDst)
	}
	requestValues := requestDst.Values("X-Trace-Id")
	if len(requestValues) != 2 || requestValues[0] != "one" || requestValues[1] != "two" {
		t.Fatalf("request copy lost multivalue header: %v", requestValues)
	}
	for _, name := range []string{"X-Connection", "X-Request-Controlled", "X-Proxy-Controlled"} {
		if requestDst.Get(name) != "" {
			t.Errorf("request Connection-nominated header %q was copied: %v", name, requestDst)
		}
	}
}
