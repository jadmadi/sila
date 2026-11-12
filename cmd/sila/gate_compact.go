package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/importmem"
	"github.com/jadmadi/sila/internal/render"
)

const (
	gateCompactMaxLines = 12
	gateReceiptMaxBytes = 10 << 20
	gateReceiptKeep     = 5
	gateReceiptDirRel   = ".sila/gate"
)

// gateCompactMode resolves compact versus detail output. Explicit flags win,
// SILA_GATE_COMPACT=0 forces detail, and a non-TTY defaults to compact.
func gateCompactMode(explicitCompact, explicitVerbose, isTTY bool) (bool, error) {
	if explicitCompact && explicitVerbose {
		return false, fmt.Errorf("--compact and --verbose are mutually exclusive")
	}
	if explicitCompact {
		return true, nil
	}
	if explicitVerbose {
		return false, nil
	}
	if strings.TrimSpace(os.Getenv("SILA_GATE_COMPACT")) == "0" {
		return false, nil
	}
	return !isTTY, nil
}

func gateIsTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func gateCheckDuration(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	return time.Duration(ms * int64(time.Millisecond)).Round(time.Millisecond).String()
}

// renderGateCompact prints one line per failing or skipped check and a bounded
// number of passing lines, then the summary and receipt path.
func renderGateCompact(ui render.UI, checks []gateCheck, verdict gateVerdict, receiptPath string, elapsed time.Duration) {
	passLines := gateCompactMaxLines - 2
	if passLines < 1 {
		passLines = 1
	}
	passes, problems := 0, []gateCheck{}
	for _, c := range checks {
		switch c.Status {
		case "fail", "skip", "partial", "unavailable":
			problems = append(problems, c)
		default:
			passes++
		}
	}
	printedPass := 0
	for _, c := range checks {
		if c.Status != "fail" && c.Status != "skip" && c.Status != "partial" && c.Status != "unavailable" {
			if printedPass >= passLines {
				continue
			}
			printedPass++
			fmt.Fprintf(os.Stdout, "PASS %s %s\n", c.Name, gateCheckDuration(c.DurationMS))
			continue
		}
		status := "SKIP"
		if c.Status == "fail" {
			status = "FAIL"
		}
		fmt.Fprintf(os.Stdout, "%s %s %s\n", status, c.Name, gateCheckDuration(c.DurationMS))
		if c.Status == "fail" {
			if c.Detail != "" {
				fmt.Fprintf(os.Stdout, "  %s\n", c.Detail)
			}
			if c.Output != "" {
				for _, line := range strings.Split(strings.TrimRight(c.Output, "\n"), "\n") {
					fmt.Fprintf(os.Stdout, "  %s\n", line)
				}
			}
		}
	}
	if remaining := passes - printedPass; remaining > 0 {
		fmt.Fprintf(os.Stdout, "PASS %d more checks\n", remaining)
	}
	_ = problems
	if verdict.Clean {
		fmt.Fprintf(os.Stdout, "PASS %d checks in %s; full log: %s\n", len(checks), elapsed.Round(time.Millisecond), receiptPath)
	} else {
		fmt.Fprintf(os.Stdout, "FAIL %d of %d checks in %s; full log: %s\n", len(verdict.Blockers), len(checks), elapsed.Round(time.Millisecond), receiptPath)
	}
}

// writeGateRunReceipt persists every check and its captured output under
// .sila/gate, copies the newest file to last-run.log, and keeps five runs.
// Failures print one line and never change the exit code.
func writeGateRunReceipt(cwd string, checks []gateCheck, verdict gateVerdict) string {
	dir := filepath.Join(cwd, filepath.FromSlash(gateReceiptDirRel))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "gate receipt: %v\n", err)
		return ""
	}
	body := renderGateReceipt(checks, verdict)
	if redacted, redactErr := importmem.Redact(body, cwd); redactErr == nil {
		body = redacted.Text
	} else {
		fmt.Fprintf(os.Stderr, "gate receipt: redaction failed: %v\n", redactErr)
		return ""
	}
	base := fmt.Sprintf("run-%d", time.Now().UnixNano())
	path := filepath.Join(dir, base+".log")
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.log", base, i))
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "gate receipt: %v\n", err)
		return ""
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "gate receipt: %v\n", err)
		return ""
	}
	last := filepath.Join(dir, "last-run.log")
	lastTmp := last + ".tmp"
	if err := os.WriteFile(lastTmp, []byte(body), 0o600); err == nil {
		_ = os.Rename(lastTmp, last)
	}
	pruneGateReceipts(dir)
	return path
}

func renderGateReceipt(checks []gateCheck, verdict gateVerdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sila gate receipt\ntier: pre-commit\nclean: %v\nexit: %d\n\n", verdict.Clean, verdict.ExitCode)
	for _, c := range checks {
		fmt.Fprintf(&b, "[%s] %s (%s)\n", strings.ToUpper(c.Status), c.Name, gateCheckDuration(c.DurationMS))
		if c.Detail != "" {
			fmt.Fprintf(&b, "detail: %s\n", c.Detail)
		}
		if c.Warning != "" {
			fmt.Fprintf(&b, "warning: %s\n", c.Warning)
		}
		if c.Output != "" {
			fmt.Fprintf(&b, "output:\n%s\n", truncateGateStream(c.Output))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func truncateGateStream(s string) string {
	if len(s) <= gateReceiptMaxBytes {
		return s
	}
	return s[:gateReceiptMaxBytes] + "\n[truncated at 10 MiB]\n"
}

func pruneGateReceipts(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "run-*.log"))
	if err != nil || len(matches) <= gateReceiptKeep {
		return
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-gateReceiptKeep] {
		_ = os.Remove(old)
	}
}
