package pluginbridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDeterministicAndValidJS(t *testing.T) {
	raw := RawTemplate()
	if !strings.Contains(raw, "{{HARNESS}}") || !strings.Contains(raw, "{{VERSION}}") {
		t.Fatalf("raw template missing {{HARNESS}} or {{VERSION}} placeholders")
	}

	oc1, err := Render("opencode", "v1.2.3")
	if err != nil {
		t.Fatalf("Render opencode failed: %v", err)
	}
	oc2, err := Render("open-code", "v1.2.3")
	if err != nil {
		t.Fatalf("Render open-code failed: %v", err)
	}
	if oc1 != oc2 {
		t.Fatalf("Render is not deterministic across alias open-code")
	}
	if strings.Contains(oc1, "{{HARNESS}}") || strings.Contains(oc1, "{{VERSION}}") {
		t.Fatalf("rendered opencode bridge still has unresolved placeholders")
	}
	if !strings.Contains(oc1, `const HARNESS = "opencode";`) {
		t.Fatalf("expected HARNESS = opencode in rendered output")
	}

	mc, err := Render("mimocode", "v1.2.3")
	if err != nil {
		t.Fatalf("Render mimocode failed: %v", err)
	}
	if !strings.Contains(mc, `const HARNESS = "mimocode";`) {
		t.Fatalf("expected HARNESS = mimocode in rendered output")
	}

	if _, err := Render("claude", "v1.2.3"); err == nil {
		t.Fatalf("expected error rendering plugin bridge for shell-hook harness claude")
	}

	// Verify no forbidden eval, network, or npm imports exist in the rendered template
	for _, forbidden := range []string{"eval(", "new Function(", "fetch(", "http:", "https:"} {
		if strings.Contains(oc1, forbidden) {
			t.Fatalf("rendered bridge contains forbidden token %q", forbidden)
		}
	}

	// Run node --check if node is available
	if nodeBin, err := exec.LookPath("node"); err == nil {
		tmpDir := t.TempDir()
		for name, content := range map[string]string{
			"opencode-bridge.mjs": oc1,
			"mimocode-bridge.mjs": mc,
		} {
			p := filepath.Join(tmpDir, name)
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
			cmd := exec.Command(nodeBin, "--check", p)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("node --check failed for %s: %v\n%s", name, err, string(out))
			}
		}
	}
}
