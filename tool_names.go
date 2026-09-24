package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

const (
	maxFunctionToolNameLength     = 64
	functionToolAliasPrefix       = "muse_"
	functionToolAliasDigestLength = maxFunctionToolNameLength - len(functionToolAliasPrefix) - 1
)

type toolNameAliases struct {
	toUpstream map[string]string
	toClient   map[string]string
}

func buildToolNameAliases(request map[string]any) (*toolNameAliases, error) {
	return buildToolNameAliasesWithLimit(request, maxFunctionToolNameLength)
}

func buildToolNameAliasesWithLimit(request map[string]any, maxBytes int) (*toolNameAliases, error) {
	if maxBytes == 0 {
		return nil, nil
	}
	if maxBytes < 16 || maxBytes > 256 {
		return nil, errors.New("invalid function tool name length limit")
	}

	names := make(map[string]struct{})
	collectToolNames(request["tools"], names)
	if input, ok := request["input"].([]any); ok {
		for _, item := range input {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch entry["type"] {
			case "additional_tools":
				collectToolNames(entry["tools"], names)
			case "function_call":
				if name, ok := entry["name"].(string); ok {
					names[name] = struct{}{}
				}
			}
		}
	}
	if choice, ok := request["tool_choice"].(map[string]any); ok && choice["type"] == "function" {
		if name, ok := choice["name"].(string); ok {
			names[name] = struct{}{}
		}
	}

	aliases := &toolNameAliases{
		toUpstream: make(map[string]string),
		toClient:   make(map[string]string),
	}
	for name := range names {
		if len(name) <= maxBytes {
			continue
		}
		alias, err := functionToolAliasWithLimit(name, maxBytes)
		if err != nil {
			return nil, err
		}
		if _, collision := names[alias]; collision {
			return nil, errors.New("function tool alias collides with an existing name")
		}
		if previous, collision := aliases.toClient[alias]; collision && previous != name {
			return nil, errors.New("function tool alias collision")
		}
		aliases.toUpstream[name] = alias
		aliases.toClient[alias] = name
	}
	if len(aliases.toUpstream) == 0 {
		return nil, nil
	}
	return aliases, nil
}

func functionToolAlias(name string) string {
	alias, _ := functionToolAliasWithLimit(name, maxFunctionToolNameLength)
	return alias
}

func functionToolAliasWithLimit(name string, maxBytes int) (string, error) {
	if maxBytes < len(functionToolAliasPrefix)+1 {
		return "", errors.New("function tool alias limit is too small")
	}
	digestLength := maxBytes - len(functionToolAliasPrefix)
	if digestLength > functionToolAliasDigestLength {
		digestLength = functionToolAliasDigestLength
	}
	digest := sha256.Sum256([]byte(name))
	return functionToolAliasPrefix + hex.EncodeToString(digest[:])[:digestLength], nil
}

func collectToolNames(value any, names map[string]struct{}) {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			collectToolNames(child, names)
		}
	case map[string]any:
		if node["type"] == "function" {
			if name, ok := node["name"].(string); ok {
				names[name] = struct{}{}
			}
		}
		if nested, exists := node["tools"]; exists {
			collectToolNames(nested, names)
		}
	}
}

func (m *toolNameAliases) rewriteRequest(request map[string]any) {
	if m == nil {
		return
	}
	if tools, exists := request["tools"]; exists {
		rewriteToolTreeNames(tools, m.toUpstream)
	}
	if input, ok := request["input"].([]any); ok {
		for _, item := range input {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch entry["type"] {
			case "additional_tools":
				rewriteToolTreeNames(entry["tools"], m.toUpstream)
			case "function_call":
				rewriteNamedFunction(entry, m.toUpstream)
			}
		}
	}
	if choice, ok := request["tool_choice"].(map[string]any); ok && choice["type"] == "function" {
		rewriteNamedFunction(choice, m.toUpstream)
	}
}

func rewriteToolTreeNames(value any, mapping map[string]string) {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			rewriteToolTreeNames(child, mapping)
		}
	case map[string]any:
		if node["type"] == "function" {
			rewriteNamedFunction(node, mapping)
		}
		if nested, exists := node["tools"]; exists {
			rewriteToolTreeNames(nested, mapping)
		}
	}
}

func rewriteNamedFunction(function map[string]any, mapping map[string]string) {
	name, ok := function["name"].(string)
	if !ok {
		return
	}
	if alias, exists := mapping[name]; exists {
		function["name"] = alias
	}
}

func (m *toolNameAliases) hasAliases() bool {
	return m != nil && len(m.toUpstream) > 0
}
