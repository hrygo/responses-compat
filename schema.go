package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
)

const (
	maxRequestBytes = 32 << 20
	maxSchemaDepth  = 64
	museModel       = "muse-spark-1.3-contributor"
)

func NormalizeRequest(data []byte) ([]byte, error) {
	normalized, _, err := normalizeRequestWithToolNames(data)
	return normalized, err
}

func normalizeRequestWithToolNames(data []byte) ([]byte, *toolNameAliases, error) {
	if len(data) == 0 || len(data) > maxRequestBytes {
		return nil, nil, errors.New("request size out of range")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var request map[string]any
	if err := decoder.Decode(&request); err != nil {
		return nil, nil, errors.New("invalid JSON request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, nil, errors.New("request contains trailing JSON")
	}
	if request == nil || request["model"] != museModel {
		return nil, nil, errors.New("unsupported model")
	}

	aliases, err := buildToolNameAliases(request)
	if err != nil {
		return nil, nil, err
	}
	aliases.rewriteRequest(request)

	budget := &schemaBudget{limit: maxRequestBytes}
	if tools, exists := request["tools"]; exists {
		if err := normalizeToolTree(tools, budget); err != nil {
			return nil, nil, err
		}
	}
	if input, ok := request["input"].([]any); ok {
		for _, item := range input {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if entry["type"] == "reasoning" {
				// The OpenCode Go route may not retain provider-scoped reasoning IDs across tool turns.
				// Keep encrypted_content and summary for stateless replay; remove only the unstable ID.
				delete(entry, "id")
			}
			if entry["type"] != "additional_tools" {
				continue
			}
			if tools, exists := entry["tools"]; exists {
				if err := normalizeToolTree(tools, budget); err != nil {
					return nil, nil, err
				}
			}
		}
	}

	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(request); err != nil {
		return nil, nil, errors.New("could not encode normalized request")
	}
	encoded := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	if len(encoded) > maxRequestBytes {
		return nil, nil, errors.New("normalized request exceeds size limit")
	}
	return append([]byte(nil), encoded...), aliases, nil
}

type schemaBudget struct {
	used  int
	limit int
}

func (b *schemaBudget) add(size int) error {
	if size < 0 || b.used > b.limit-size {
		return errors.New("expanded schema exceeds size limit")
	}
	b.used += size
	return nil
}

func encodedJSONStringSize(value string, budget *schemaBudget) (int, error) {
	if len(value) > budget.limit-budget.used {
		return 0, errors.New("expanded schema exceeds size limit")
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return 0, errors.New("could not encode schema string")
	}
	return encoded.Len() - 1, nil
}

func normalizeToolTree(value any, budget *schemaBudget) error {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			if err := normalizeToolTree(child, budget); err != nil {
				return err
			}
		}
	case map[string]any:
		if rawSchema, exists := node["parameters"]; exists {
			schema, ok := rawSchema.(map[string]any)
			if !ok {
				return errors.New("tool parameters must be a JSON object")
			}
			normalized, err := expandSchemaNode(schema, schema, make(map[string]bool), 0, budget)
			if err != nil {
				return err
			}
			node["parameters"] = normalized
		}
		if nested, exists := node["tools"]; exists {
			return normalizeToolTree(nested, budget)
		}
	}
	return nil
}

func resolveJSONPointer(root any, ref string) (any, string, error) {
	if !strings.HasPrefix(ref, "#") {
		return nil, "", errors.New("non-local schema reference")
	}
	fragment, err := url.PathUnescape(ref[1:])
	if err != nil {
		return nil, "", errors.New("invalid URI fragment in schema reference")
	}
	if fragment == "" {
		return root, "#", nil
	}
	if !strings.HasPrefix(fragment, "/") {
		return nil, "", errors.New("invalid JSON pointer fragment")
	}
	canonical := "#" + fragment
	current := root
	for _, encoded := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		for i := 0; i < len(encoded); i++ {
			if encoded[i] == '~' {
				if i+1 >= len(encoded) || (encoded[i+1] != '0' && encoded[i+1] != '1') {
					return nil, "", errors.New("invalid JSON pointer escape")
				}
				i++
			}
		}
		part := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			next, exists := node[part]
			if !exists {
				return nil, "", errors.New("schema reference is unresolved")
			}
			current = next
		case []any:
			if part == "" || (len(part) > 1 && part[0] == '0') {
				return nil, "", errors.New("invalid JSON pointer array index")
			}
			for _, digit := range part {
				if digit < '0' || digit > '9' {
					return nil, "", errors.New("invalid JSON pointer array index")
				}
			}
			index, err := strconv.Atoi(part)
			if err != nil || index >= len(node) {
				return nil, "", errors.New("schema reference is unresolved")
			}
			current = node[index]
		default:
			return nil, "", errors.New("schema reference target is not traversable")
		}
	}
	return current, canonical, nil
}

func expandRef(ref string, root any, active map[string]bool, depth int, budget *schemaBudget) (any, error) {
	if depth > maxSchemaDepth {
		return nil, errors.New("schema depth exceeded")
	}
	target, canonical, err := resolveJSONPointer(root, ref)
	if err != nil {
		return nil, err
	}
	if active[canonical] {
		if err := budget.add(2); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	}
	active[canonical] = true
	value, err := expandSchemaNode(target, root, active, depth+1, budget)
	delete(active, canonical)
	return value, err
}

func expandSchemaNode(value, root any, active map[string]bool, depth int, budget *schemaBudget) (any, error) {
	if depth > maxSchemaDepth {
		return nil, errors.New("schema depth exceeded")
	}
	switch node := value.(type) {
	case map[string]any:
		if rawRef, exists := node["$ref"]; exists {
			ref, ok := rawRef.(string)
			if !ok {
				return nil, errors.New("schema reference must be a string")
			}
			for key := range node {
				if key != "$ref" && key != "$defs" && key != "definitions" {
					return nil, errors.New("schema reference node has unsupported siblings")
				}
			}
			return expandRef(ref, root, active, depth+1, budget)
		}
		out := make(map[string]any, len(node))
		if err := budget.add(2); err != nil {
			return nil, err
		}
		first := true
		for key, child := range node {
			if key == "$defs" || key == "definitions" {
				continue
			}
			keySize, err := encodedJSONStringSize(key, budget)
			if err != nil {
				return nil, err
			}
			separatorSize := keySize + 1
			if !first {
				separatorSize++
			}
			if err := budget.add(separatorSize); err != nil {
				return nil, err
			}
			first = false
			expanded, err := expandSchemaNode(child, root, active, depth+1, budget)
			if err != nil {
				return nil, err
			}
			out[key] = expanded
		}
		return out, nil
	case []any:
		out := make([]any, len(node))
		if err := budget.add(2); err != nil {
			return nil, err
		}
		for i, child := range node {
			if i > 0 {
				if err := budget.add(1); err != nil {
					return nil, err
				}
			}
			expanded, err := expandSchemaNode(child, root, active, depth+1, budget)
			if err != nil {
				return nil, err
			}
			out[i] = expanded
		}
		return out, nil
	default:
		if text, ok := node.(string); ok {
			size, err := encodedJSONStringSize(text, budget)
			if err != nil {
				return nil, err
			}
			return value, budget.add(size)
		}
		encoded, err := json.Marshal(node)
		if err != nil {
			return nil, errors.New("could not encode schema value")
		}
		if err := budget.add(len(encoded)); err != nil {
			return nil, err
		}
		return value, nil
	}
}
