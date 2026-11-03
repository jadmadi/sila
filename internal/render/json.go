package render

import (
	"encoding/json"
	"io"
	"reflect"
)

// JSONSchemaV1 is the versioned schema identifier for the machine envelope.
const JSONSchemaV1 = "sila/json/v1"

// JSONError is the stable error object carried by an error envelope.
type JSONError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Envelope is the stable machine-readable envelope emitted by --json-envelope
// and by every error path that produces JSON.
type Envelope struct {
	Schema    string     `json:"schema"`
	OK        bool       `json:"ok"`
	Data      any        `json:"data"`
	Warnings  []string   `json:"warnings"`
	Error     *JSONError `json:"error"`
	Mutations []string   `json:"mutations"`
}

// NewSuccessEnvelope builds a success envelope with non-nil collections.
func NewSuccessEnvelope(data any, warnings, mutations []string) Envelope {
	if warnings == nil {
		warnings = []string{}
	}
	if mutations == nil {
		mutations = []string{}
	}
	return Envelope{
		Schema:    JSONSchemaV1,
		OK:        true,
		Data:      NormalizeSlices(data),
		Warnings:  warnings,
		Error:     nil,
		Mutations: mutations,
	}
}

// NewErrorEnvelope builds an error envelope with a stable code and message.
func NewErrorEnvelope(code, message, fix string) Envelope {
	return Envelope{
		Schema:    JSONSchemaV1,
		OK:        false,
		Data:      nil,
		Warnings:  []string{},
		Error:     &JSONError{Code: code, Message: message, Fix: fix},
		Mutations: []string{},
	}
}

// EmitJSONSuccess writes a success envelope to w.
func EmitJSONSuccess(w io.Writer, data any, warnings, mutations []string) error {
	return writeJSON(w, NewSuccessEnvelope(data, warnings, mutations))
}

// EmitJSONError writes an error envelope to w.
func EmitJSONError(w io.Writer, code, message, fix string) error {
	return writeJSON(w, NewErrorEnvelope(code, message, fix))
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// NormalizeSlices returns a copy of v in which every nil slice, at any depth,
// is replaced by an empty slice. Encoding then emits [] instead of null for
// list-shaped fields. Cyclic structures are not expected in JSON payloads;
// recursion is bounded to prevent an accidental infinite walk.
func NormalizeSlices(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	out := normalizeValue(rv, 0)
	if !out.IsValid() || !out.CanInterface() {
		return v
	}
	return out.Interface()
}

func normalizeValue(v reflect.Value, depth int) reflect.Value {
	if depth > 32 || !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		inner := normalizeValue(v.Elem(), depth+1)
		if !inner.IsValid() || !inner.Type().Implements(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(inner)
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(normalizeValue(v.Elem(), depth+1))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.MakeSlice(v.Type(), 0, 0)
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(normalizeValue(v.Index(i), depth+1))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(normalizeValue(v.Index(i), depth+1))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), normalizeValue(iter.Value(), depth+1))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			field := out.Field(i)
			if !field.CanSet() {
				continue
			}
			field.Set(normalizeValue(field, depth+1))
		}
		return out
	default:
		return v
	}
}
