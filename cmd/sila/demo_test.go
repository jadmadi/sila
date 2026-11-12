package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/demo"
	"github.com/jadmadi/sila/internal/render"
)

func testUI() render.UI { return render.New(os.Stdout, true) }

func TestRunDemoWalkthroughDefault(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runDemoCommand(testUI(), 0, "127.0.0.1", true, false, false, false, false, false, "all"); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	})
	for i := 1; i <= 7; i++ {
		if !strings.Contains(out, itoa(i)+"/7 ") {
			t.Errorf("output missing stage %d/7", i)
		}
	}
	if !strings.Contains(out, "Summary") {
		t.Error("output missing summary table")
	}
	if strings.Contains(out, "Sandbox kept") {
		t.Error("default run must not keep the sandbox")
	}
}

func TestRunDemoWalkthroughJSON(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runDemoCommand(testUI(), 0, "127.0.0.1", true, false, true, false, false, false, "all"); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	})
	var rep demo.WalkthroughReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if rep.Version != demo.WalkthroughVersion {
		t.Errorf("version = %q, want %q", rep.Version, demo.WalkthroughVersion)
	}
	if len(rep.Stages) != 7 {
		t.Errorf("stages = %d, want 7", len(rep.Stages))
	}
	if !rep.CleanedUp {
		t.Error("cleaned_up = false, want true")
	}
	if !rep.Ephemeral {
		t.Error("ephemeral = false, want true")
	}
}

func TestRunDemoWalkthroughTOON(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runDemoCommand(testUI(), 0, "127.0.0.1", true, false, false, true, false, false, "all"); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "demo_stages[7]{index,id,command,status,summary}:") {
		t.Errorf("unexpected TOON output:\n%s", out)
	}
	for _, id := range demo.CanonicalStageOrder {
		if !strings.Contains(out, id) {
			t.Errorf("TOON output missing %s", id)
		}
	}
}

func TestRunDemoWalkthroughStepFilter(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runDemoCommand(testUI(), 0, "127.0.0.1", true, false, false, false, false, false, "recall,prime,gate"); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	})
	for _, want := range []string{"3/7 prime", "4/7 recall", "6/7 gate"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	for _, absent := range []string{"1/7 sync", "2/7 salam", "5/7 facts", "7/7 standup"} {
		if strings.Contains(out, absent) {
			t.Errorf("output should not contain %q", absent)
		}
	}
}

func TestRunDemoWalkthroughInvalidStep(t *testing.T) {
	var code int
	errOut := captureStderr(t, func() {
		code = runDemoCommand(testUI(), 0, "127.0.0.1", true, false, false, false, false, false, "bogus")
	})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, `unknown --step value "bogus"`) {
		t.Errorf("stderr = %q, want unknown step message", errOut)
	}
	if !strings.Contains(errOut, "valid:") {
		t.Errorf("stderr should list valid stages: %q", errOut)
	}
}

func TestRunDemoWalkthroughKeep(t *testing.T) {
	var rep demo.WalkthroughReport
	out := captureStdout(t, func() {
		if code := runDemoCommand(testUI(), 0, "127.0.0.1", true, false, true, false, false, true, "all"); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	})
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if rep.CleanedUp {
		t.Error("cleaned_up = true, want false with --keep")
	}
	if rep.Ephemeral {
		t.Error("ephemeral = true, want false with --keep")
	}
	defer os.RemoveAll(rep.SandboxDir)
	fi, err := os.Stat(rep.SandboxDir)
	if err != nil {
		t.Fatalf("kept sandbox missing: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("sandbox mode = %o, want 700", perm)
	}
	if !strings.Contains(out, rep.StorePath) {
		t.Error("report should expose the kept store path")
	}

	// Human mode must print copy-pasteable follow-up commands.
	human := captureStdout(t, func() {
		if code := runDemoCommand(testUI(), 0, "127.0.0.1", true, false, false, false, false, true, "prime"); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	})
	if !strings.Contains(human, "--db=") || !strings.Contains(human, "rm -rf") {
		t.Errorf("kept output missing live commands:\n%s", human)
	}
	// Clean up the second kept sandbox printed in human output.
	for _, line := range strings.Split(human, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "dir: ") {
			os.RemoveAll(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "dir: ")))
		}
	}
}

func TestRunDemoWalkthroughFlagConflicts(t *testing.T) {
	cases := []struct {
		name string
		keep bool
		step string
	}{
		{name: "keep with web", keep: true, step: "all"},
		{name: "step with web", keep: false, step: "prime"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			errOut := captureStderr(t, func() {
				code = runDemoCommand(testUI(), -1, "127.0.0.1", true, false, false, false, true, tc.keep, tc.step)
			})
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(errOut, "--keep and --step cannot be combined with --web") {
				t.Errorf("stderr = %q", errOut)
			}
		})
	}
}

func TestRunDemoWebCockpitMode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // start and stop in one pass: startup must still succeed.

	out := captureStdout(t, func() {
		if err := runDemoWebContext(ctx, testUI(), -1, "127.0.0.1", true, false, true); err != nil {
			t.Fatalf("web cockpit: %v", err)
		}
	})
	if !strings.Contains(out, `"status": "running"`) {
		t.Errorf("web cockpit did not report running: %s", out)
	}
}

func TestWebCockpitStaticAlignment(t *testing.T) {
	path := filepath.Join("..", "..", "internal", "web", "static", "index.html")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	html := string(data)
	for _, want := range []string{
		"Add raw SQL query in checkout route handler for speed",
		"Add second frontend framework alongside the SPA",
		"Key refund webhook writes on (order_id, provider_event_id)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing atlas-web proposal %q", want)
		}
	}
	for _, gone := range []string{"Implement CGO SQLite driver", "Support export rules for Neovim and Zed editors"} {
		if strings.Contains(html, gone) {
			t.Errorf("index.html still contains Sila-internal chip %q", gone)
		}
	}
	for _, tool := range []string{"cursor", "windsurf", "cline", "zed", "zcode"} {
		if !strings.Contains(html, `value="`+tool+`"`) {
			t.Errorf("index.html search selector missing harness %q", tool)
		}
	}
}

// itoa avoids importing strconv for a single digit.
func itoa(n int) string {
	return string(rune('0' + n))
}
