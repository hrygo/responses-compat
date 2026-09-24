package main

import "errors"

const (
	normalizationCodeInvalidRequest              = "invalid_request"
	normalizationCodeSchemaRefSiblingUnsupported = "schema_ref_sibling_unsupported"
)

type normalizationError struct {
	Code    string
	Message string
}

func (e *normalizationError) Error() string {
	if e == nil {
		return normalizationCodeInvalidRequest
	}
	return e.Message
}

func normalizationCode(err error) string {
	var classified *normalizationError
	if errors.As(err, &classified) && classified != nil {
		switch classified.Code {
		case normalizationCodeSchemaRefSiblingUnsupported:
			return classified.Code
		}
	}
	return normalizationCodeInvalidRequest
}
