package config

import (
	"encoding/json"
	"reflect"

	"github.com/invopop/jsonschema"
)

//go:generate go run ../../cmd/snipe-sync schema --output ../../snipe-sync.schema.json

// JSONSchema returns the editor-facing structural schema generated from Config's YAML tags.
func JSONSchema() *jsonschema.Schema {
	durationType := reflect.TypeFor[Duration]()
	reflector := &jsonschema.Reflector{
		FieldNameTag: "yaml",
		Mapper: func(valueType reflect.Type) *jsonschema.Schema {
			if valueType == durationType {
				return &jsonschema.Schema{
					Type:        "string",
					Description: "A Go duration such as 1m, 15m, or 1h.",
					Examples:    []any{"1m", "15m", "1h"},
				}
			}
			return nil
		},
	}
	schema := reflector.Reflect(&Config{})
	schema.ID = jsonschema.ID("https://raw.githubusercontent.com/woodleighschool/snipe-sync/main/snipe-sync.schema.json")
	schema.Title = "Configuration"
	schema.Description = "Provider connections and typed Snipe-IT reconciliation policy."
	return schema
}

// JSONSchemaDocument returns the generated schema as stable indented JSON.
func JSONSchemaDocument() ([]byte, error) {
	document, err := json.MarshalIndent(JSONSchema(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(document, '\n'), nil
}
