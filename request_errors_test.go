package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestNormalizationCodeUsesFixedWrappedCode(t *testing.T) {
	wrapped := fmt.Errorf("outer context: %w", &normalizationError{Code: "schema_ref_sibling_unsupported", Message: "safe message"})
	if got := normalizationCode(wrapped); got != "schema_ref_sibling_unsupported" {
		t.Fatalf("code=%q", got)
	}
	for _, err := range []error{errors.New("private payload and pointer"), &normalizationError{Code: "private_dynamic_code", Message: "secret"}} {
		if got := normalizationCode(err); got != "invalid_request" {
			t.Fatalf("unknown error code=%q", got)
		}
	}
}
