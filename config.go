package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const maxConfigBytes = 1 << 20

type RuntimeConfig struct {
	Listen               string
	UpstreamBaseURL      string
	Models               []string
	Profile              string
	Policy               CompatibilityPolicy
	ExtraRequestHeaders  []string
	ExtraResponseHeaders []string
}

type configFile struct {
	ConfigVersion        int             `json:"config_version"`
	Listen               string          `json:"listen"`
	UpstreamBaseURL      string          `json:"upstream_base_url"`
	Models               []string        `json:"models"`
	Profile              string          `json:"profile"`
	Transforms           json.RawMessage `json:"transforms"`
	ExtraRequestHeaders  json.RawMessage `json:"extra_request_headers"`
	ExtraResponseHeaders json.RawMessage `json:"extra_response_headers"`
}

type transformConfigFile struct {
	SchemaRefs       json.RawMessage `json:"schema_refs"`
	RecursiveRefs    json.RawMessage `json:"recursive_refs"`
	ToolNameMaxBytes json.RawMessage `json:"tool_name_max_bytes"`
	ReasoningIDs     json.RawMessage `json:"reasoning_ids"`
}

func defaultConfig() RuntimeConfig {
	return RuntimeConfig{
		Listen:               listenAddress,
		UpstreamBaseURL:      upstreamBase,
		Models:               []string{museModel},
		Profile:              profileMuse,
		Policy:               musePolicy(),
		ExtraRequestHeaders:  []string{},
		ExtraResponseHeaders: []string{},
	}
}

func loadConfig(path string) (RuntimeConfig, error) {
	if path == "" {
		return defaultConfig(), nil
	}
	file, err := os.Open(path)
	if err != nil {
		return RuntimeConfig{}, errors.New("could not open configuration file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return RuntimeConfig{}, errors.New("could not read configuration file")
	}
	if len(data) > maxConfigBytes {
		return RuntimeConfig{}, errors.New("configuration file exceeds size limit")
	}
	return decodeConfig(data)
}

func decodeConfig(data []byte) (RuntimeConfig, error) {
	if len(data) == 0 || len(data) > maxConfigBytes {
		return RuntimeConfig{}, errors.New("configuration size out of range")
	}
	var raw configFile
	if err := decodeStrictJSON(data, &raw); err != nil {
		return RuntimeConfig{}, err
	}
	if raw.ConfigVersion != 1 {
		return RuntimeConfig{}, errors.New("unsupported configuration version")
	}

	var policy CompatibilityPolicy
	switch raw.Profile {
	case profileMuse:
		policy = musePolicy()
	case profilePassthrough:
		policy = passthroughPolicy()
	default:
		return RuntimeConfig{}, errors.New("unknown compatibility profile")
	}
	if len(raw.Transforms) != 0 {
		if isJSONNull(raw.Transforms) {
			return RuntimeConfig{}, errors.New("transforms must be an object")
		}
		var overrides transformConfigFile
		if err := decodeStrictJSON(raw.Transforms, &overrides); err != nil {
			return RuntimeConfig{}, err
		}
		if err := applyTransformOverrides(&policy, overrides); err != nil {
			return RuntimeConfig{}, err
		}
	}

	extraRequestHeaders, err := decodeExtraHeaderNames(raw.ExtraRequestHeaders, "request")
	if err != nil {
		return RuntimeConfig{}, err
	}
	extraResponseHeaders, err := decodeExtraHeaderNames(raw.ExtraResponseHeaders, "response")
	if err != nil {
		return RuntimeConfig{}, err
	}
	config := RuntimeConfig{
		Listen:               raw.Listen,
		UpstreamBaseURL:      raw.UpstreamBaseURL,
		Models:               append([]string(nil), raw.Models...),
		Profile:              raw.Profile,
		Policy:               policy,
		ExtraRequestHeaders:  extraRequestHeaders,
		ExtraResponseHeaders: extraResponseHeaders,
	}
	if err := validateRuntimeConfig(config); err != nil {
		return RuntimeConfig{}, err
	}
	return cloneRuntimeConfig(config), nil
}

func applyTransformOverrides(policy *CompatibilityPolicy, overrides transformConfigFile) error {
	if err := decodeStringOverride(overrides.SchemaRefs, &policy.SchemaRefs); err != nil {
		return errors.New("invalid schema_refs transform")
	}
	if err := decodeStringOverride(overrides.RecursiveRefs, &policy.RecursiveRefs); err != nil {
		return errors.New("invalid recursive_refs transform")
	}
	if len(overrides.ToolNameMaxBytes) != 0 {
		if isJSONNull(overrides.ToolNameMaxBytes) {
			policy.ToolNameMaxBytes = 0
		} else {
			var limit int
			if err := json.Unmarshal(overrides.ToolNameMaxBytes, &limit); err != nil {
				return errors.New("invalid tool_name_max_bytes transform")
			}
			if limit == 0 {
				return errors.New("tool_name_max_bytes must be null to disable aliases")
			}
			policy.ToolNameMaxBytes = limit
		}
	}
	if err := decodeStringOverride(overrides.ReasoningIDs, &policy.ReasoningIDs); err != nil {
		return errors.New("invalid reasoning_ids transform")
	}
	if err := validateCompatibilityPolicy(*policy); err != nil {
		return errors.New("invalid compatibility transform policy")
	}
	return nil
}

func decodeStringOverride(raw json.RawMessage, destination *string) error {
	if len(raw) == 0 {
		return nil
	}
	if isJSONNull(raw) {
		return errors.New("string override must be non-null")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return errors.New("override must be a string")
	}
	*destination = value
	return nil
}

func decodeExtraHeaderNames(raw json.RawMessage, direction string) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	if isJSONNull(raw) {
		return nil, errors.New("extra header list must be an array")
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil || names == nil {
		return nil, errors.New("extra header list must be an array of names")
	}
	normalized, err := normalizeExtraHeaderNames(names, direction)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func validateRuntimeConfig(config RuntimeConfig) error {
	if _, err := validateListenAddress(config.Listen); err != nil {
		return err
	}
	if _, err := parseUpstreamBaseURL(config.UpstreamBaseURL); err != nil {
		return err
	}
	if len(config.Models) == 0 {
		return errors.New("models must be a non-empty allowlist")
	}
	seenModels := make(map[string]struct{}, len(config.Models))
	for _, model := range config.Models {
		if strings.TrimSpace(model) == "" {
			return errors.New("model IDs must be non-empty")
		}
		if _, exists := seenModels[model]; exists {
			return errors.New("model IDs must not contain duplicates")
		}
		seenModels[model] = struct{}{}
	}
	if config.Profile != profileMuse && config.Profile != profilePassthrough {
		return errors.New("unknown compatibility profile")
	}
	if err := validateCompatibilityPolicy(config.Policy); err != nil {
		return errors.New("invalid compatibility policy")
	}
	if _, err := normalizeExtraHeaderNames(config.ExtraRequestHeaders, "request"); err != nil {
		return err
	}
	if _, err := normalizeExtraHeaderNames(config.ExtraResponseHeaders, "response"); err != nil {
		return err
	}
	return nil
}

func validateListenAddress(address string) (string, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("listen must be a literal loopback IP and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("listen must use a literal loopback IP")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("listen port is invalid")
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
}

func parseUpstreamBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("upstream_base_url must be an absolute URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return nil, errors.New("upstream_base_url must not contain credentials, query, or fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("HTTP upstream is allowed only for a literal loopback IP")
		}
	default:
		return nil, errors.New("upstream_base_url scheme must be HTTPS")
	}
	if portText := parsed.Port(); portText != "" {
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("upstream port is invalid")
		}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return parsed, nil
}

func decodeStrictJSON(data []byte, destination any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid configuration JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("configuration contains trailing data")
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanConfigJSONValue(decoder); err != nil {
		return errors.New("invalid configuration JSON")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("configuration contains trailing data")
	}
	return nil
}

func scanConfigJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid object key")
			}
			canonicalKey := strings.ToLower(key)
			if _, exists := seen[canonicalKey]; exists {
				return errors.New("duplicate object key")
			}
			seen[canonicalKey] = struct{}{}
			if err := scanConfigJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid object boundary")
		}
	case '[':
		for decoder.More() {
			if err := scanConfigJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid array boundary")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func cloneRuntimeConfig(config RuntimeConfig) RuntimeConfig {
	config.Models = append([]string(nil), config.Models...)
	config.ExtraRequestHeaders = append([]string(nil), config.ExtraRequestHeaders...)
	config.ExtraResponseHeaders = append([]string(nil), config.ExtraResponseHeaders...)
	return config
}
