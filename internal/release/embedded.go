package release

import (
	_ "embed"
	"os"
	"strings"
)

//go:embed CHANGELOG.md
var embeddedChangelog string

// LoadChangelogContent loads the changelog markdown from local disk if available,
// falling back to the embedded compile-time changelog.
func LoadChangelogContent() string {
	candidates := []string{
		"CHANGELOG.md",
		"cli/CHANGELOG.md",
		"../CHANGELOG.md",
		"../../CHANGELOG.md",
	}
	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			content := string(data)
			if strings.Contains(content, "# Changelog") && strings.Contains(content, "sila") {
				return content
			}
		}
	}
	return embeddedChangelog
}
