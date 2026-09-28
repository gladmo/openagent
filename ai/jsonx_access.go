package ai

// jsonx_access.go: small typed accessors over decoded jsonx values shared by
// the API clients (catalog compat metadata and provider wire payloads).

import "github.com/gladmo/openagent/jsonx"

// JxObj casts a jsonx value to an object.
func JxObj(v any) (*jsonx.Obj, bool) {
	obj, ok := v.(*jsonx.Obj)
	return obj, ok
}

// JxString reads a string field.
func JxString(o *jsonx.Obj, key string) (string, bool) {
	if o == nil {
		return "", false
	}
	value, ok := o.Get(key)
	if !ok {
		return "", false
	}
	s, isString := value.(string)
	return s, isString
}

// JxFloat reads a number field.
func JxFloat(o *jsonx.Obj, key string) (float64, bool) {
	if o == nil {
		return 0, false
	}
	value, ok := o.Get(key)
	if !ok {
		return 0, false
	}
	f, isNumber := jsonx.ToFloat(value)
	return f, isNumber
}

// JxBool reads a boolean field.
func JxBool(o *jsonx.Obj, key string) (bool, bool) {
	if o == nil {
		return false, false
	}
	value, ok := o.Get(key)
	if !ok {
		return false, false
	}
	b, isBool := value.(bool)
	return b, isBool
}

// JxObjectField reads a nested object field.
func JxObjectField(o *jsonx.Obj, key string) (*jsonx.Obj, bool) {
	if o == nil {
		return nil, false
	}
	value, ok := o.Get(key)
	if !ok {
		return nil, false
	}
	return JxObj(value)
}

// JxList reads an array field.
func JxList(o *jsonx.Obj, key string) ([]any, bool) {
	if o == nil {
		return nil, false
	}
	value, ok := o.Get(key)
	if !ok {
		return nil, false
	}
	list, isList := value.([]any)
	return list, isList
}

// JxCompatObj returns the model's compat object when present.
func JxCompatObj(model *Model) (*jsonx.Obj, bool) {
	if model == nil {
		return nil, false
	}
	return JxObj(model.Compat)
}

// JxCompatBool reads a compat boolean with a default.
func JxCompatBool(model *Model, key string, fallback bool) bool {
	if compat, ok := JxCompatObj(model); ok {
		if value, present := JxBool(compat, key); present {
			return value
		}
	}
	return fallback
}
