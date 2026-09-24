package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func validConfigJSON(profile, extra string) []byte {
	return []byte(`{"config_version":1,"listen":"127.0.0.1:18317","upstream_base_url":"https://opencode.ai/zen/go/v1","models":["muse-spark-1.3-contributor"],"profile":"` + profile + `"` + extra + `}`)
}

func TestDecodeConfigDefaultsAndNullAliasOverride(t *testing.T) {
	defaulted, err := decodeConfig(validConfigJSON("muse", ""))
	if err != nil {
		t.Fatal(err)
	}
	if defaulted.Policy != musePolicy() {
		t.Fatalf("default policy=%+v want=%+v", defaulted.Policy, musePolicy())
	}
	nullOverride, err := decodeConfig(validConfigJSON("muse", `,"transforms":{"tool_name_max_bytes":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if nullOverride.Policy.ToolNameMaxBytes != 0 || nullOverride.Policy.SchemaRefs != "inline" || nullOverride.Policy.ReasoningIDs != "drop" {
		t.Fatalf("null override did not disable only aliases: %+v", nullOverride.Policy)
	}
	passthrough, err := decodeConfig(validConfigJSON("passthrough", ""))
	if err != nil {
		t.Fatal(err)
	}
	if passthrough.Policy != passthroughPolicy() {
		t.Fatalf("passthrough policy=%+v want=%+v", passthrough.Policy, passthroughPolicy())
	}
}

func TestDecodeConfigRejectsDuplicateKeysAtEveryDepth(t *testing.T) {
	cases := map[string]string{
		"root":              `{"config_version":1,"config_version":1}`,
		"nested transforms": `{"config_version":1,"listen":"127.0.0.1:18317","upstream_base_url":"https://opencode.ai/zen/go/v1","models":["muse-spark-1.3-contributor"],"profile":"muse","transforms":{"schema_refs":"inline","schema_refs":"preserve"}}`,
		"nested object":     `{"config_version":1,"listen":"127.0.0.1:18317","upstream_base_url":"https://opencode.ai/zen/go/v1","models":["muse-spark-1.3-contributor"],"profile":"muse","extra":{"x":1,"x":2}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeConfig([]byte(raw)); err == nil {
				t.Fatal("duplicate key was accepted")
			}
		})
	}
}

func TestDecodeConfigRejectsUnknownFieldsTrailingDataAndInvalidOverrides(t *testing.T) {
	cases := map[string][]byte{
		"unknown root":            validConfigJSON("muse", `,"unexpected":true`),
		"unknown transform":       validConfigJSON("muse", `,"transforms":{"unknown":"value"}`),
		"trailing JSON":           append(validConfigJSON("muse", ""), []byte(` {}`)...),
		"missing profile":         []byte(`{"config_version":1,"listen":"127.0.0.1:18317","upstream_base_url":"https://opencode.ai/zen/go/v1","models":["muse-spark-1.3-contributor"]}`),
		"unknown profile":         validConfigJSON("other", ""),
		"wrong version":           []byte(strings.Replace(string(validConfigJSON("muse", "")), `"config_version":1`, `"config_version":2`, 1)),
		"zero alias length":       validConfigJSON("muse", `,"transforms":{"tool_name_max_bytes":0}`),
		"fractional alias length": validConfigJSON("muse", `,"transforms":{"tool_name_max_bytes":16.5}`),
		"too-small alias length":  validConfigJSON("muse", `,"transforms":{"tool_name_max_bytes":15}`),
		"too-large alias length":  validConfigJSON("muse", `,"transforms":{"tool_name_max_bytes":257}`),
		"null transform object":   validConfigJSON("muse", `,"transforms":null`),
		"null schema policy":      validConfigJSON("muse", `,"transforms":{"schema_refs":null}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeConfig(raw); err == nil {
				t.Fatal("invalid config was accepted")
			}
		})
	}
}

func TestDecodeConfigRejectsInvalidListenersAndUpstreams(t *testing.T) {
	base := string(validConfigJSON("muse", ""))
	cases := map[string]string{
		"external listener IP":  strings.Replace(base, `127.0.0.1:18317`, `0.0.0.0:18317`, 1),
		"listener hostname":     strings.Replace(base, `127.0.0.1:18317`, `localhost:18317`, 1),
		"invalid listener port": strings.Replace(base, `127.0.0.1:18317`, `127.0.0.1:70000`, 1),
		"upstream credentials":  strings.Replace(base, `https://opencode.ai/zen/go/v1`, `https://user:pass@opencode.ai/zen/go/v1`, 1),
		"upstream query":        strings.Replace(base, `https://opencode.ai/zen/go/v1`, `https://opencode.ai/zen/go/v1?x=1`, 1),
		"upstream fragment":     strings.Replace(base, `https://opencode.ai/zen/go/v1`, `https://opencode.ai/zen/go/v1#fragment`, 1),
		"non-loopback HTTP":     strings.Replace(base, `https://opencode.ai/zen/go/v1`, `http://example.com/v1`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeConfig([]byte(raw)); err == nil {
				t.Fatal("invalid listener or upstream was accepted")
			}
		})
	}
}

func TestDecodeConfigValidatesAndDeduplicatesHeaderAllowlists(t *testing.T) {
	cfg, err := decodeConfig(validConfigJSON("passthrough", `,"extra_request_headers":["X-Trace-Id","x-trace-id","X-Request-Tag"],"extra_response_headers":["X-Upstream-Trace","x-upstream-trace"]`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(cfg.ExtraRequestHeaders, ","), "X-Trace-Id,X-Request-Tag"; got != want {
		t.Fatalf("request header allowlist=%q want=%q", got, want)
	}
	if got, want := strings.Join(cfg.ExtraResponseHeaders, ","), "X-Upstream-Trace"; got != want {
		t.Fatalf("response header allowlist=%q want=%q", got, want)
	}
	for name, extra := range map[string]string{
		"invalid token":           `,"extra_request_headers":["Bad Header"]`,
		"connection":              `,"extra_request_headers":["Connection"]`,
		"controlled content type": `,"extra_request_headers":["Content-Type"]`,
		"response content length": `,"extra_response_headers":["Content-Length"]`,
		"null list":               `,"extra_response_headers":null`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := validConfigJSON("passthrough", extra)
			if _, err := decodeConfig(raw); err == nil {
				t.Fatal("invalid or unsafe header allowlist was accepted")
			}
		})
	}
}

func TestLoadConfigEmptyPathDefaultsAndMissingPathFails(t *testing.T) {
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != listenAddress || cfg.UpstreamBaseURL != upstreamBase || cfg.Policy != musePolicy() {
		t.Fatalf("default config=%+v", cfg)
	}
	if _, err := loadConfig(t.TempDir() + "/missing.json"); err == nil {
		t.Fatal("missing config silently fell back to defaults")
	}
}

func TestExampleConfigsAreValidAndCredentialFree(t *testing.T) {
	cases := []struct {
		path    string
		profile string
		listen  string
	}{
		{path: "examples/muse.json", profile: "muse", listen: "127.0.0.1:18317"},
		{path: "examples/muse-passthrough.json", profile: "passthrough", listen: "127.0.0.1:18318"},
	}
	for _, test := range cases {
		t.Run(test.profile, func(t *testing.T) {
			config, err := loadConfig(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if config.Profile != test.profile || config.Listen != test.listen {
				t.Fatalf("config profile/listen=%q/%q", config.Profile, config.Listen)
			}
			if strings.Contains(strings.ToLower(config.UpstreamBaseURL), "token") || strings.Contains(strings.ToLower(config.UpstreamBaseURL), "secret") {
				t.Fatal("example contains credential-like upstream URL data")
			}
		})
	}
}

func TestConfiguredHandlersKeepUpstreamsAndModelAllowlistIsolated(t *testing.T) {
	type observed struct {
		mu    sync.Mutex
		token string
		calls int
	}
	var first, second observed
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first.mu.Lock()
		first.token = r.Header.Get("Authorization")
		first.calls++
		first.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"from-a"}`)
	}))
	defer upstreamA.Close()
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second.mu.Lock()
		second.token = r.Header.Get("Authorization")
		second.calls++
		second.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"from-b"}`)
	}))
	defer upstreamB.Close()

	configA := defaultConfig()
	configA.UpstreamBaseURL = upstreamA.URL + "/v1"
	configA.Models = []string{"model-a"}
	configA.Policy = passthroughPolicy()
	configB := defaultConfig()
	configB.UpstreamBaseURL = upstreamB.URL + "/v1"
	configB.Models = []string{"model-b"}
	configB.Policy = passthroughPolicy()
	handlerA, err := NewConfiguredHandler(configA, upstreamA.Client())
	if err != nil {
		t.Fatal(err)
	}
	handlerB, err := NewConfiguredHandler(configB, upstreamB.Client())
	if err != nil {
		t.Fatal(err)
	}

	serve := func(handler http.Handler, model, token string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"`+model+`","input":"ping"}`))
		request.Header.Set("Authorization", token)
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if got := serve(handlerA, "model-a", "Bearer A"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"from-a"`) {
		t.Fatalf("handler A response: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := serve(handlerB, "model-b", "Bearer B"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"from-b"`) {
		t.Fatalf("handler B response: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := serve(handlerB, "model-a", "Bearer wrong"); got.Code != http.StatusBadRequest {
		t.Fatalf("handler B accepted model outside its allowlist: status=%d", got.Code)
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	second.mu.Lock()
	defer second.mu.Unlock()
	if first.calls != 1 || first.token != "Bearer A" || second.calls != 1 || second.token != "Bearer B" {
		t.Fatalf("configuration crossed instances: first=(calls=%d token=%q) second=(calls=%d token=%q)", first.calls, first.token, second.calls, second.token)
	}
}

func TestConfiguredHandlerCopiesConfigSlices(t *testing.T) {
	forwarded := make(chan http.Header, 1)
	config := defaultConfig()
	config.UpstreamBaseURL = "https://upstream.example/v1"
	config.Models = []string{"model-a"}
	config.Policy = passthroughPolicy()
	config.Profile = "passthrough"
	config.ExtraRequestHeaders = []string{"X-Trace-Id"}
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		forwarded <- request.Header.Clone()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`)), Request: request}, nil
	})}
	handler, err := NewConfiguredHandler(config, client)
	if err != nil {
		t.Fatal(err)
	}
	config.Models[0] = "model-b"
	config.ExtraRequestHeaders[0] = "X-Other"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"model-a","input":"ping"}`))
	request.Header.Set("X-Trace-Id", "original-config")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case headers := <-forwarded:
		if got := headers.Get("X-Trace-Id"); got != "original-config" {
			t.Fatalf("handler observed caller-mutated config: X-Trace-Id=%q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copied model allowlist was not used")
	}
}

func TestConfigDecodingDoesNotAcceptTopLevelNonObjects(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `true`, `"text"`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := decodeConfig([]byte(raw)); err == nil {
				t.Fatal("non-object config was accepted")
			}
		})
	}
}

func TestRunCommandMissingConfigDoesNotStartServer(t *testing.T) {
	started := false
	err := runCommand([]string{"--config", t.TempDir() + "/missing.json"}, io.Discard, func(RuntimeConfig) error {
		started = true
		return nil
	})
	if err == nil {
		t.Fatal("missing --config path did not fail")
	}
	if started {
		t.Fatal("server startup callback ran after config loading failed")
	}
}

func TestRunCommandRejectsUnexpectedArguments(t *testing.T) {
	started := false
	err := runCommand([]string{"unexpected"}, io.Discard, func(RuntimeConfig) error {
		started = true
		return nil
	})
	if err == nil {
		t.Fatal("unexpected positional argument was accepted")
	}
	if started {
		t.Fatal("server startup callback ran after flag parsing failed")
	}
}
