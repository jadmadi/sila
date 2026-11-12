package pluginbridge

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed template.js
var rawOpenCodeTemplate string

//go:embed template.mimocode.js
var rawMiMoCodeTemplate string

// DefaultBridgeVersion is the schema/template revision of the shared JS bridge.
const DefaultBridgeVersion = "v1"

// BridgeAPIMarkerV1 marks a rendered bridge that carries the OpenCode 1
// plugin shape (default export async function returning a hook map).
const BridgeAPIMarkerV1 = "sila:plugin-api=1"

// BridgeAPIMarkerV2 marks a rendered bridge that carries the OpenCode 2
// plugin definition (default export object with an id and a setup function).
const BridgeAPIMarkerV2 = "sila:plugin-api=2"

// BridgeAPIMarkerFor returns the plugin API marker a rendered bridge for the
// harness must carry.
func BridgeAPIMarkerFor(harness string) string {
	if normalizeHarness(harness) == "opencode" {
		return BridgeAPIMarkerV2
	}
	return BridgeAPIMarkerV1
}

// Render generates the plain JavaScript bridge source for a plugin-only harness
// ("opencode" or "mimocode") by substituting {{HARNESS}} and {{VERSION}}.
// OpenCode renders the V2-only template; MiMoCode renders the V1-only template.
func Render(harness, version string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(harness))
	var source string
	switch h {
	case "opencode", "open-code":
		h = "opencode"
		source = rawOpenCodeTemplate
	case "mimocode", "mimo-code", "mimo":
		h = "mimocode"
		source = rawMiMoCodeTemplate
	default:
		return "", fmt.Errorf("pluginbridge: unsupported plugin harness %q", harness)
	}
	v := strings.TrimSpace(version)
	if v == "" {
		v = DefaultBridgeVersion
	}
	out := strings.ReplaceAll(source, "{{HARNESS}}", h)
	out = strings.ReplaceAll(out, "{{VERSION}}", v)
	return out, nil
}

// RawTemplate returns the unrendered OpenCode template string.
func RawTemplate() string {
	return rawOpenCodeTemplate
}

// RawMiMoCodeTemplate returns the unrendered MiMoCode template string.
func RawMiMoCodeTemplate() string {
	return rawMiMoCodeTemplate
}
