package ai

import "github.com/gladmo/openagent/typebox"

// typeboxSchema aliases the typebox Schema for internal helpers.
type typeboxSchema = typebox.Schema

// typeboxSchemaFromJSON decodes a raw JSON-Schema object into a Schema.
func typeboxSchemaFromJSON(obj any) *typebox.Schema {
	return typebox.SchemaFromJSON(obj)
}
