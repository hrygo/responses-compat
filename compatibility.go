package main

import "errors"

const (
	museModel                = "muse-spark-1.3-contributor"
	profileMuse              = "muse"
	profilePassthrough       = "passthrough"
	schemaRefsInline         = "inline"
	schemaRefsPreserve       = "preserve"
	recursiveRefsReject      = "reject"
	recursiveRefsEmptySchema = "empty_schema"
	reasoningIDsDrop         = "drop"
	reasoningIDsPreserve     = "preserve"
)

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
		SchemaRefs:       schemaRefsInline,
		RecursiveRefs:    recursiveRefsEmptySchema,
		ToolNameMaxBytes: maxFunctionToolNameLength,
		ReasoningIDs:     reasoningIDsDrop,
	}
}

func passthroughPolicy() CompatibilityPolicy {
	return CompatibilityPolicy{
		SchemaRefs:       schemaRefsPreserve,
		RecursiveRefs:    recursiveRefsReject,
		ToolNameMaxBytes: 0,
		ReasoningIDs:     reasoningIDsPreserve,
	}
}

func validateCompatibilityPolicy(policy CompatibilityPolicy) error {
	if policy.SchemaRefs != schemaRefsPreserve && policy.SchemaRefs != schemaRefsInline {
		return errors.New("invalid compatibility policy")
	}
	if policy.RecursiveRefs != recursiveRefsReject && policy.RecursiveRefs != recursiveRefsEmptySchema {
		return errors.New("invalid compatibility policy")
	}
	if policy.ToolNameMaxBytes != 0 && (policy.ToolNameMaxBytes < 16 || policy.ToolNameMaxBytes > 256) {
		return errors.New("invalid compatibility policy")
	}
	if policy.ReasoningIDs != reasoningIDsPreserve && policy.ReasoningIDs != reasoningIDsDrop {
		return errors.New("invalid compatibility policy")
	}
	return nil
}
