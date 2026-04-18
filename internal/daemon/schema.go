package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// CompileSchema compiles an output_schema value into a *jsonschema.Schema.
// Returns nil if schema is the string "text" (no validation needed).
func CompileSchema(outputSchema any) (*jsonschema.Schema, error) {
	switch v := outputSchema.(type) {
	case string:
		if v == "text" {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected output_schema string %q", v)
	case map[string]any:
		c := jsonschema.NewCompiler()
		if err := c.AddResource("schema.json", v); err != nil {
			return nil, fmt.Errorf("compile output_schema: %w", err)
		}
		return c.Compile("schema.json")
	default:
		return nil, fmt.Errorf("output_schema has unexpected type %T", v)
	}
}

// ValidateOutput checks that output conforms to schema.
// If schema is nil (text mode), validation is skipped.
func ValidateOutput(schema *jsonschema.Schema, output string) error {
	if schema == nil {
		return nil
	}

	var v any
	if err := json.Unmarshal([]byte(output), &v); err != nil {
		return fmt.Errorf("output is not valid JSON: %w", err)
	}

	if err := schema.Validate(v); err != nil {
		return fmt.Errorf("output does not match schema: %w", err)
	}
	return nil
}
