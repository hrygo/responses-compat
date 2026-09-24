package main

import (
	"encoding/json"
	"errors"
	"reflect"
)

func expandRefWithSiblings(node map[string]any, root any, active map[string]bool, depth int, budget *schemaBudget) (any, error) {
	return expandRefNodeWithRecursiveRefs(node, root, active, depth, budget, recursiveRefsEmptySchema)
}

func expandRefNodeWithRecursiveRefs(node map[string]any, root any, active map[string]bool, depth int, budget *schemaBudget, recursiveRefs string) (any, error) {
	rawRef, exists := node["$ref"]
	if !exists {
		return nil, unsupportedRefSibling()
	}
	ref, ok := rawRef.(string)
	if !ok {
		return nil, errors.New("schema reference must be a string")
	}

	siblingCount := 0
	for key := range node {
		if key != "$ref" && key != "$defs" && key != "definitions" {
			siblingCount++
		}
	}
	if siblingCount == 0 {
		return expandRefWithRecursiveRefs(ref, root, active, depth, budget, recursiveRefs)
	}

	// Keep intermediate work bounded independently from the caller budget. The
	// merged value is charged by its final serialized size below, so an adjacent
	// annotation may replace a longer annotation on the target without being
	// incorrectly rejected for the discarded bytes.
	targetBudget := &schemaBudget{limit: maxRequestBytes}
	expandedTarget, canonical, entered, err := expandRefTargetWithRecursiveRefs(ref, root, active, depth, targetBudget, recursiveRefs)
	if err != nil {
		return nil, err
	}
	if entered {
		defer delete(active, canonical)
	}

	target, ok := expandedTarget.(map[string]any)
	if !ok {
		return nil, unsupportedRefSibling()
	}
	merged := make(map[string]any, len(target)+siblingCount)
	for key, value := range target {
		merged[key] = value
	}

	siblingBudget := &schemaBudget{limit: maxRequestBytes}
	for key, rawSibling := range node {
		if key == "$ref" || key == "$defs" || key == "definitions" {
			continue
		}
		if key == "description" || key == "title" || key == "$comment" {
			annotation, ok := rawSibling.(string)
			if !ok {
				return nil, unsupportedRefSibling()
			}
			merged[key] = annotation
			continue
		}

		if existing, exists := merged[key]; exists && reflect.DeepEqual(existing, rawSibling) {
			continue
		}
		expandedSibling, err := expandSchemaNodeWithRecursiveRefs(rawSibling, root, active, depth+1, siblingBudget, recursiveRefs)
		if err != nil {
			return nil, err
		}
		if existing, exists := merged[key]; exists && reflect.DeepEqual(existing, expandedSibling) {
			continue
		}
		return nil, unsupportedRefSibling()
	}

	finalSize, err := expandedSchemaSize(merged)
	if err != nil {
		return nil, err
	}
	if err := budget.add(finalSize); err != nil {
		return nil, err
	}
	return merged, nil
}

func expandedSchemaSize(value any) (int, error) {
	budget := &schemaBudget{limit: maxRequestBytes}
	if err := measureExpandedSchema(value, budget); err != nil {
		return 0, err
	}
	return budget.used, nil
}

func measureExpandedSchema(value any, budget *schemaBudget) error {
	switch node := value.(type) {
	case map[string]any:
		if err := budget.add(2); err != nil {
			return err
		}
		first := true
		for key, child := range node {
			keySize, err := encodedJSONStringSize(key, &schemaBudget{limit: budget.limit})
			if err != nil {
				return err
			}
			memberSize := keySize + 1
			if !first {
				memberSize++
			}
			if err := budget.add(memberSize); err != nil {
				return err
			}
			first = false
			if err := measureExpandedSchema(child, budget); err != nil {
				return err
			}
		}
		return nil
	case []any:
		if err := budget.add(2); err != nil {
			return err
		}
		for index, child := range node {
			if index > 0 {
				if err := budget.add(1); err != nil {
					return err
				}
			}
			if err := measureExpandedSchema(child, budget); err != nil {
				return err
			}
		}
		return nil
	case string:
		size, err := encodedJSONStringSize(node, &schemaBudget{limit: budget.limit})
		if err != nil {
			return err
		}
		return budget.add(size)
	default:
		encoded, err := json.Marshal(node)
		if err != nil {
			return errors.New("could not encode expanded schema value")
		}
		return budget.add(len(encoded))
	}
}

func unsupportedRefSibling() error {
	return &normalizationError{
		Code:    normalizationCodeSchemaRefSiblingUnsupported,
		Message: "unsupported constraint beside schema reference",
	}
}
