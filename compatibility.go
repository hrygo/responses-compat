package main

import "errors"

// CompatibilityPolicy controls independent, explicit Responses request transforms.
// A zero ToolNameMaxBytes disables tool-name aliasing.
type CompatibilityPolicy struct {
	SchemaRefs       string
	RecursiveRefs    string
	ToolNameMaxBytes int
	ReasoningIDs     string
}

func musePolicy() CompatibilityPolicy {
	return CompatibilityPolicy{
		SchemaRefs:       "inline",
		RecursiveRefs:    "empty_schema",
		ToolNameMaxBytes: maxFunctionToolNameLength,
		ReasoningIDs:     "drop",
	}
}

func passthroughPolicy() CompatibilityPolicy {
	return CompatibilityPolicy{
		SchemaRefs:       "preserve",
		RecursiveRefs:    "reject",
		ToolNameMaxBytes: 0,
		ReasoningIDs:     "preserve",
	}
}

func validateCompatibilityPolicy(policy CompatibilityPolicy) error {
	if policy.SchemaRefs != "preserve" && policy.SchemaRefs != "inline" {
		return errors.New("invalid compatibility policy")
	}
	if policy.RecursiveRefs != "reject" && policy.RecursiveRefs != "empty_schema" {
		return errors.New("invalid compatibility policy")
	}
	if policy.ToolNameMaxBytes != 0 && (policy.ToolNameMaxBytes < 16 || policy.ToolNameMaxBytes > 256) {
		return errors.New("invalid compatibility policy")
	}
	if policy.ReasoningIDs != "preserve" && policy.ReasoningIDs != "drop" {
		return errors.New("invalid compatibility policy")
	}
	return nil
}
