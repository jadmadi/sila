package pluginbridge

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed template.js
var rawTemplate string

// DefaultBridgeVersion is the schema/template revision of the shared JS bridge.
const DefaultBridgeVersion = "v1"

// Render generates the plain JavaScript bridge source for a plugin-only harness
// ("opencode" or "mimocode") by substituting {{HARNESS}} and {{VERSION}}.
func Render(harness, version string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(harness))
	switch h {
	case "opencode", "open-code":
		h = "opencode"
	case "mimocode", "mimo-code", "mimo":
		h = "mimocode"
	default:
		return "", fmt.Errorf("pluginbridge: unsupported plugin harness %q", harness)
	}
	v := strings.TrimSpace(version)
	if v == "" {
		v = DefaultBridgeVersion
	}
	out := strings.ReplaceAll(rawTemplate, "{{HARNESS}}", h)
	out = strings.ReplaceAll(out, "{{VERSION}}", v)
	return out, nil
}

// RawTemplate returns the unrendered template.js string.
func RawTemplate() string {
	return rawTemplate
}
