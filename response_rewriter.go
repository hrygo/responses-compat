package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	maxResponseRewriteBytes = 64 << 20
)

var (
	errResponseRewriteLimit = errors.New("rewritten response exceeds size limit")
	errInvalidJSONResponse  = errors.New("invalid JSON response")
)

func restoreToolNamesInJSON(data []byte, aliases map[string]string, maxOutputBytes int) ([]byte, bool, error) {
	return restoreToolNames(data, aliases, maxOutputBytes, "", true)
}

func restoreToolNamesInSSEJSON(data []byte, aliases map[string]string, maxOutputBytes int, eventType string) ([]byte, bool, error) {
	return restoreToolNames(data, aliases, maxOutputBytes, eventType, false)
}

type toolNameReplacement struct {
	object   map[string]any
	original string
}

func restoreToolNames(data []byte, aliases map[string]string, maxOutputBytes int, eventType string, responseJSON bool) ([]byte, bool, error) {
	if len(aliases) == 0 {
		return data, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var response any
	if err := decoder.Decode(&response); err != nil {
		return nil, false, errInvalidJSONResponse
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false, errInvalidJSONResponse
	}

	targets := collectToolNameTargets(response, eventType, responseJSON)
	replacements := make([]toolNameReplacement, 0, len(targets))
	for _, target := range targets {
		name, ok := target["name"].(string)
		if !ok {
			continue
		}
		original, exists := aliases[name]
		if exists && original != name {
			replacements = append(replacements, toolNameReplacement{object: target, original: original})
		}
	}
	if len(replacements) == 0 {
		return data, false, nil
	}

	finalSize, err := encodedJSONSize(response)
	if err != nil {
		return nil, false, err
	}
	for _, replacement := range replacements {
		name := replacement.object["name"].(string)
		finalSize, err = replaceEncodedJSONStringSize(finalSize, name, replacement.original)
		if err != nil {
			return nil, false, err
		}
	}
	if maxOutputBytes < 0 || finalSize > int64(maxOutputBytes) {
		return nil, false, errResponseRewriteLimit
	}

	for _, replacement := range replacements {
		replacement.object["name"] = replacement.original
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		return nil, false, err
	}
	encoded := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	if len(encoded) > maxOutputBytes {
		return nil, false, errResponseRewriteLimit
	}
	return encoded, true, nil
}

func collectToolNameTargets(value any, eventType string, responseJSON bool) []map[string]any {
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	targets := make([]map[string]any, 0)
	if responseJSON {
		rootType, _ := root["type"].(string)
		if rootType == "function_call" {
			appendToolNameTarget(&targets, root)
		}
		if isResponseLifecycleEvent(rootType) {
			if response, ok := root["response"].(map[string]any); ok {
				collectResponseToolNameTargets(response, &targets)
			}
			return targets
		}
		collectResponseToolNameTargets(root, &targets)
		return targets
	}

	if eventType == "" {
		eventType, _ = root["type"].(string)
	}
	switch eventType {
	case "response.output_item.added", "response.output_item.done":
		if item, ok := root["item"]; ok {
			collectFunctionCallTargets(item, &targets)
			collectFunctionDefinitionTargets(item, &targets)
		}
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		appendToolNameTarget(&targets, root)
	default:
		if isResponseLifecycleEvent(eventType) {
			if response, ok := root["response"].(map[string]any); ok {
				collectResponseToolNameTargets(response, &targets)
			}
		}
	}
	return targets
}

func isResponseLifecycleEvent(eventType string) bool {
	switch eventType {
	case "response.created", "response.in_progress", "response.completed", "response.incomplete", "response.failed":
		return true
	default:
		return false
	}
}

func collectResponseToolNameTargets(response map[string]any, targets *[]map[string]any) {
	if output, exists := response["output"]; exists {
		collectFunctionCallTargets(output, targets)
	}
	if tools, exists := response["tools"]; exists {
		collectFunctionDefinitionTargets(tools, targets)
	}
	if namespace, exists := response["namespace"]; exists {
		collectFunctionDefinitionTargets(namespace, targets)
	}
}

func collectFunctionCallTargets(value any, targets *[]map[string]any) {
	switch node := value.(type) {
	case []any:
		for _, item := range node {
			collectFunctionCallTargets(item, targets)
		}
	case map[string]any:
		if node["type"] == "function_call" {
			appendToolNameTarget(targets, node)
		}
	}
}

func collectFunctionDefinitionTargets(value any, targets *[]map[string]any) {
	switch node := value.(type) {
	case []any:
		for _, item := range node {
			collectFunctionDefinitionTargets(item, targets)
		}
	case map[string]any:
		switch node["type"] {
		case "function":
			appendToolNameTarget(targets, node)
		case "namespace":
			if tools, exists := node["tools"]; exists {
				collectFunctionDefinitionTargets(tools, targets)
			}
		}
	}
}

func appendToolNameTarget(targets *[]map[string]any, object map[string]any) {
	if _, ok := object["name"].(string); ok {
		*targets = append(*targets, object)
	}
}

func encodedJSONSize(value any) (int64, error) {
	const maxSize = int64(1<<63 - 1)
	add := func(current, amount int64) (int64, error) {
		if amount < 0 || current > maxSize-amount {
			return 0, errResponseRewriteLimit
		}
		return current + amount, nil
	}

	switch node := value.(type) {
	case nil:
		return 4, nil
	case bool:
		if node {
			return 4, nil
		}
		return 5, nil
	case json.Number:
		return int64(len(node)), nil
	case string:
		return encodedJSONQuotedStringSize(node), nil
	case []any:
		size := int64(2)
		for index, child := range node {
			if index > 0 {
				var err error
				size, err = add(size, 1)
				if err != nil {
					return 0, err
				}
			}
			childSize, err := encodedJSONSize(child)
			if err != nil {
				return 0, err
			}
			size, err = add(size, childSize)
			if err != nil {
				return 0, err
			}
		}
		return size, nil
	case map[string]any:
		size := int64(2)
		index := 0
		for key, child := range node {
			if index > 0 {
				var err error
				size, err = add(size, 1)
				if err != nil {
					return 0, err
				}
			}
			keySize, err := add(encodedJSONQuotedStringSize(key), 1)
			if err != nil {
				return 0, err
			}
			size, err = add(size, keySize)
			if err != nil {
				return 0, err
			}
			childSize, err := encodedJSONSize(child)
			if err != nil {
				return 0, err
			}
			size, err = add(size, childSize)
			if err != nil {
				return 0, err
			}
			index++
		}
		return size, nil
	default:
		return 0, errors.New("unsupported value in Responses JSON")
	}
}

func replaceEncodedJSONStringSize(size int64, previous, replacement string) (int64, error) {
	previousSize := encodedJSONQuotedStringSize(previous)
	replacementSize := encodedJSONQuotedStringSize(replacement)
	const maxSize = int64(1<<63 - 1)
	if replacementSize >= previousSize {
		delta := replacementSize - previousSize
		if size > maxSize-delta {
			return 0, errResponseRewriteLimit
		}
		return size + delta, nil
	}
	delta := previousSize - replacementSize
	if delta > size {
		return 0, errResponseRewriteLimit
	}
	return size - delta, nil
}

// encodedJSONQuotedStringSize matches encoding/json string quoting with HTML escaping disabled.
func encodedJSONQuotedStringSize(value string) int64 {
	size := int64(2)
	for len(value) > 0 {
		r, width := utf8.DecodeRuneInString(value)
		switch r {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			size += 2
		case 0x2028, 0x2029:
			size += 6
		default:
			if r < 0x20 {
				size += 6
			} else if r == utf8.RuneError && width == 1 {
				size += 3
			} else {
				size += int64(width)
			}
		}
		value = value[width:]
	}
	return size
}
