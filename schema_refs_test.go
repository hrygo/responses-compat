package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestRefSiblingRedundantTypeAndDescription(t *testing.T) {
	body := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"example","parameters":{"type":"object","properties":{"value":{"$ref":"#/$defs/Text","type":"string","description":"synthetic annotation","title":"synthetic title","$comment":"synthetic comment"}},"$defs":{"Text":{"type":"string"}}}}]}`)
	got, err := NormalizeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `"$ref"`) {
		t.Fatal("reference not expanded")
	}
	if !strings.Contains(string(got), `"description":"synthetic annotation"`) {
		t.Fatal("annotation lost")
	}
	if !strings.Contains(string(got), `"type":"string"`) {
		t.Fatal("type lost")
	}
	if !strings.Contains(string(got), `"title":"synthetic title"`) || !strings.Contains(string(got), `"$comment":"synthetic comment"`) {
		t.Fatal("supported annotations lost")
	}
}

func TestRefSiblingUnsupportedConstraintsHaveFixedCode(t *testing.T) {
	cases := map[string]string{
		"conflicting type": `{"$ref":"#/$defs/Text","type":"number","$defs":{"Text":{"type":"string"}}}`,
		"new assertion":    `{"$ref":"#/$defs/Text","minLength":1,"$defs":{"Text":{"type":"string"}}}`,
	}
	for name, parameters := range cases {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"example","parameters":` + parameters + `}]}`)
			if _, err := NormalizeRequest(body); err == nil {
				t.Fatal("unsupported constraint combination accepted")
			} else if code := normalizationCode(err); code != normalizationCodeSchemaRefSiblingUnsupported {
				t.Fatalf("normalization code=%q", code)
			}
		})
	}
}

func TestRefSiblingAnnotationDoesNotMutateDefinition(t *testing.T) {
	definition := map[string]any{"type": "string", "description": "source"}
	defs := map[string]any{"Text": definition}
	schema := map[string]any{"$defs": defs, "$ref": "#/$defs/Text", "description": "override"}
	got, err := expandSchemaNode(schema, schema, make(map[string]bool), 0, &schemaBudget{limit: maxRequestBytes})
	if err != nil {
		t.Fatal(err)
	}
	expanded, ok := got.(map[string]any)
	if !ok || expanded["description"] != "override" {
		t.Fatalf("unexpected expanded schema: %#v", got)
	}
	if definition["description"] != "source" {
		t.Fatalf("reference target was mutated: %#v", definition)
	}
}

func TestRefSiblingAnnotationBudgetCountsFinalOutput(t *testing.T) {
	for name, originalDescription := range map[string]string{"added": "", "replaced": "a much longer source annotation"} {
		t.Run(name, func(t *testing.T) {
			definition := map[string]any{"type": "string"}
			if originalDescription != "" {
				definition["description"] = originalDescription
			}
			schema := map[string]any{
				"$defs":       map[string]any{"Text": definition},
				"$ref":        "#/$defs/Text",
				"description": "x",
			}
			wantSize := len([]byte(`{"type":"string","description":"x"}`))
			budget := &schemaBudget{limit: wantSize}
			if _, err := expandSchemaNode(schema, schema, make(map[string]bool), 0, budget); err != nil {
				t.Fatalf("exact final-output budget rejected: %v", err)
			}
			if budget.used != wantSize {
				t.Fatalf("budget used=%d want=%d", budget.used, wantSize)
			}
			tight := &schemaBudget{limit: wantSize - 1}
			if _, err := expandSchemaNode(schema, schema, make(map[string]bool), 0, tight); err == nil {
				t.Fatal("annotation exceeding final-output budget was accepted")
			}
		})
	}
}

func TestRefSiblingEquivalentNestedSchemaAfterExpansion(t *testing.T) {
	schema := map[string]any{
		"$defs": map[string]any{
			"Text": map[string]any{"type": "string"},
			"Container": map[string]any{
				"properties": map[string]any{"value": map[string]any{"$ref": "#/$defs/Text"}},
			},
		},
		"$ref":       "#/$defs/Container",
		"properties": map[string]any{"value": map[string]any{"$ref": "#/$defs/Text"}},
	}
	got, err := expandSchemaNode(schema, schema, make(map[string]bool), 0, &schemaBudget{limit: maxRequestBytes})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]any{"properties": map[string]any{"value": map[string]any{"type": "string"}}}) {
		t.Fatalf("equivalent nested schema was not preserved: %#v", got)
	}
}
