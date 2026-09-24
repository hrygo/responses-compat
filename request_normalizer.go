package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const maxRequestBytes = 32 << 20

func NormalizeRequest(data []byte) ([]byte, error) {
	normalized, _, err := normalizeRequestWithToolNames(data)
	return normalized, err
}

func normalizeRequestWithToolNames(data []byte) ([]byte, *toolNameAliases, error) {
	return normalizeRequestWithPolicy(data, []string{museModel}, musePolicy())
}

func normalizeRequestWithPolicy(data []byte, models []string, policy CompatibilityPolicy) ([]byte, *toolNameAliases, error) {
	if len(data) == 0 || len(data) > maxRequestBytes {
		return nil, nil, errors.New("request size out of range")
	}
	if err := validateCompatibilityPolicy(policy); err != nil {
		return nil, nil, err
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
	if request == nil {
		return nil, nil, errors.New("request must be a JSON object")
	}
	model, ok := request["model"].(string)
	if !ok || !modelAllowed(model, models) {
		return nil, nil, errors.New("unsupported model")
	}

	var aliases *toolNameAliases
	changed := false
	if policy.ToolNameMaxBytes != 0 {
		var err error
		aliases, err = buildToolNameAliasesWithLimit(request, policy.ToolNameMaxBytes)
		if err != nil {
			return nil, nil, err
		}
		if aliases.hasAliases() {
			aliases.rewriteRequest(request)
			changed = true
		}
	}

	budget := &schemaBudget{limit: maxRequestBytes}
	if policy.SchemaRefs == schemaRefsInline {
		if tools, exists := request["tools"]; exists {
			treeChanged, err := normalizeToolTreeWithRecursiveRefs(tools, budget, policy.RecursiveRefs)
			if err != nil {
				return nil, nil, err
			}
			changed = changed || treeChanged
		}
	}
	if input, ok := request["input"].([]any); ok {
		for _, item := range input {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if entry["type"] == "reasoning" && policy.ReasoningIDs == reasoningIDsDrop {
				if _, exists := entry["id"]; exists {
					delete(entry, "id")
					changed = true
				}
			}
			if policy.SchemaRefs != schemaRefsInline || entry["type"] != "additional_tools" {
				continue
			}
			if tools, exists := entry["tools"]; exists {
				treeChanged, err := normalizeToolTreeWithRecursiveRefs(tools, budget, policy.RecursiveRefs)
				if err != nil {
					return nil, nil, err
				}
				changed = changed || treeChanged
			}
		}
	}
	if !changed {
		return append([]byte(nil), data...), aliases, nil
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

func modelAllowed(model string, models []string) bool {
	for _, allowed := range models {
		if model == allowed {
			return true
		}
	}
	return false
}
