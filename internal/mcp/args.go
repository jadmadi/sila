package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// MaxToolArgumentsBytes bounds the raw arguments envelope accepted by the
// normalizer. The MCP server's line scanner is much larger; this is a
// per-tool sanity bound, not a protocol limit.
const MaxToolArgumentsBytes = 64 * 1024

// MaxToolArgumentProperties bounds the number of top-level properties.
const MaxToolArgumentProperties = 256

// ArgError reports a specific argument that could not be normalized or did not
// match the tool's declared schema.
type ArgError struct {
	Field    string
	Expected string
	Got      string
}

func (e *ArgError) Error() string {
	return fmt.Sprintf("argument %q: expected %s, got %s", e.Field, e.Expected, e.Got)
}

// NormalizeArguments makes the MCP arguments envelope tolerant of real harness
// proxies without changing the public schema:
//
//   - a string-encoded object is unwrapped exactly once (double-encoded
//     arguments envelopes);
//   - a value for a field declared integer/number/boolean is coerced from a
//     string when it parses as the declared type;
//   - a value for a field declared array/object is unwrapped from a
//     string-encoded JSON value;
//   - string fields are never coerced, even when they look numeric;
//   - unknown fields are preserved;
//   - null values are preserved for rejectNullArguments to reject.
func NormalizeArguments(toolName string, raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("{}"), nil
	}
	if len(raw) > MaxToolArgumentsBytes {
		return nil, &ArgError{
			Field:    "arguments",
			Expected: fmt.Sprintf("object <= %d bytes", MaxToolArgumentsBytes),
			Got:      fmt.Sprintf("%d bytes", len(raw)),
		}
	}

	// Unwrap a double-encoded arguments object once.
	if inner, ok := jsonString(raw); ok {
		trimmed := bytes.TrimSpace([]byte(inner))
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return nil, &ArgError{Field: "arguments", Expected: "object", Got: jsonValueType(trimmed)}
		}
		raw = json.RawMessage(trimmed)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, &ArgError{Field: "arguments", Expected: "object", Got: jsonValueType(trimmed)}
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, &ArgError{Field: "arguments", Expected: "object", Got: "invalid JSON"}
	}
	if len(obj) > MaxToolArgumentProperties {
		return nil, &ArgError{
			Field:    "arguments",
			Expected: fmt.Sprintf("<= %d properties", MaxToolArgumentProperties),
			Got:      fmt.Sprintf("%d properties", len(obj)),
		}
	}

	props := toolProperties(toolName)
	if len(props) == 0 {
		return json.RawMessage(trimmed), nil
	}

	changed := false
	for key, value := range obj {
		prop, ok := props[key]
		if !ok {
			continue
		}
		coerced, didCoerce, err := coerceArgument(key, prop.Type, value)
		if err != nil {
			return nil, err
		}
		if didCoerce {
			obj[key] = coerced
			changed = true
		}
	}
	if !changed {
		return json.RawMessage(trimmed), nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, &ArgError{Field: "arguments", Expected: "JSON object", Got: err.Error()}
	}
	return out, nil
}

func toolProperties(toolName string) map[string]PropertySchema {
	for _, tool := range ToolDefinitions() {
		if tool.Name == toolName {
			return tool.InputSchema.Properties
		}
	}
	return nil
}

func coerceArgument(key, declaredType string, value json.RawMessage) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return value, false, nil
	}

	switch strings.ToLower(declaredType) {
	case "integer":
		inner, ok := jsonString(trimmed)
		if !ok {
			return value, false, nil
		}
		if _, err := strconv.ParseInt(strings.TrimSpace(inner), 10, 64); err != nil {
			return nil, false, &ArgError{Field: key, Expected: "integer", Got: "string " + strconv.Quote(inner)}
		}
		return json.RawMessage(strings.TrimSpace(inner)), true, nil

	case "number":
		inner, ok := jsonString(trimmed)
		if !ok {
			return value, false, nil
		}
		if _, err := strconv.ParseFloat(strings.TrimSpace(inner), 64); err != nil {
			return nil, false, &ArgError{Field: key, Expected: "number", Got: "string " + strconv.Quote(inner)}
		}
		return json.RawMessage(strings.TrimSpace(inner)), true, nil

	case "boolean":
		inner, ok := jsonString(trimmed)
		if !ok {
			return value, false, nil
		}
		switch strings.ToLower(strings.TrimSpace(inner)) {
		case "true", "1":
			return json.RawMessage("true"), true, nil
		case "false", "0":
			return json.RawMessage("false"), true, nil
		default:
			return nil, false, &ArgError{Field: key, Expected: "boolean", Got: "string " + strconv.Quote(inner)}
		}

	case "array":
		inner, ok := jsonString(trimmed)
		if !ok {
			return value, false, nil
		}
		decoded := bytes.TrimSpace([]byte(inner))
		if len(decoded) == 0 || decoded[0] != '[' || !json.Valid(decoded) {
			return nil, false, &ArgError{Field: key, Expected: "array", Got: "string"}
		}
		return json.RawMessage(decoded), true, nil

	case "object":
		inner, ok := jsonString(trimmed)
		if !ok {
			return value, false, nil
		}
		decoded := bytes.TrimSpace([]byte(inner))
		if len(decoded) == 0 || decoded[0] != '{' || !json.Valid(decoded) {
			return nil, false, &ArgError{Field: key, Expected: "object", Got: "string"}
		}
		return json.RawMessage(decoded), true, nil
	}
	return value, false, nil
}

func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func jsonValueType(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "empty"
	}
	switch trimmed[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}
