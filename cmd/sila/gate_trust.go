package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/gate"
	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/render"
)

// gateTrustOptions carries the one-run trust flags.
type gateTrustOptions struct {
	Target     string
	Range      string
	ShowConfig bool
}

var gateTrust gateTrustOptions

func isFullCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
			return false
		}
	}
	return true
}

func gitConfigEmail(repoRoot string) string {
	out, err := exec.Command("git", "-C", repoRoot, "config", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// trustCheck reviews the configured range and returns the gate check plus a
// refusal message when the decision fails.
func trustCheck(cwd string) (gateCheck, string) {
	mgr, err := git.NewManager(cwd)
	if err != nil {
		return gateCheck{Name: "trust", Status: "pass", Detail: "not a git repository"}, ""
	}
	repoRoot := mgr.RepoRoot()
	rangeExpr := strings.TrimSpace(gateTrust.Range)
	commits, err := git.CommitsInRange(repoRoot, rangeExpr)
	if err != nil {
		return gateCheck{Name: "trust", Status: "fail", Detail: err.Error()}, ""
	}
	reviewed := make([]gate.TrustCommit, 0, len(commits))
	for _, commit := range commits {
		paths, pathErr := git.ChangedPaths(repoRoot, commit.SHA)
		if pathErr != nil {
			paths = nil
		}
		reviewed = append(reviewed, gate.TrustCommit{SHA: commit.SHA, Author: commit.Author, Email: commit.Email, Paths: paths})
	}
	trusted := map[string]bool{}
	if cfg, cfgErr := config.Load(""); cfgErr == nil {
		for _, target := range cfg.Trust.Targets {
			trusted[strings.ToLower(target)] = true
		}
	}
	if gateTrust.Target != "" {
		trusted[strings.ToLower(gateTrust.Target)] = true
	}
	selfEmail := gitConfigEmail(repoRoot)
	decision := gate.EvaluateTrust(reviewed, selfEmail, trusted, nil)
	if !decision.Passed() {
		return gateCheck{Name: "trust", Status: "fail", Detail: fmt.Sprintf("%d untrusted commit(s)", len(decision.Refusals))}, renderTrustRefusal(decision, rangeExpr)
	}
	return gateCheck{Name: "trust", Status: "pass", Detail: fmt.Sprintf("%d commit(s) trusted as %s", decision.Checked, selfEmail)}, ""
}

// runGateTrustGuard validates the flag, runs the check, and exits 2 on refusal.
func runGateTrustGuard(ui render.UI, jsonOut bool) gateCheck {
	if gateTrust.Target != "" && !isFullCommitSHA(gateTrust.Target) {
		fmt.Fprintln(os.Stderr, "sila gate: --trust-target must be a full 40-character commit SHA")
		os.Exit(2)
	}
	cwd, _ := os.Getwd()
	check, refusal := trustCheck(cwd)
	if check.Status == "fail" {
		if jsonOut {
			emitJSON(gateReport{Clean: false, Checks: []gateCheck{check}})
		} else if refusal != "" {
			fmt.Fprint(os.Stderr, refusal)
		} else {
			fmt.Fprintf(os.Stderr, "%s trust check failed: %s\n", ui.Red("✖"), check.Detail)
		}
		os.Exit(2)
	}
	return check
}

func renderTrustRefusal(decision gate.TrustDecision, rangeExpr string) string {
	if rangeExpr == "" {
		rangeExpr = "HEAD"
	}
	var b strings.Builder
	b.WriteString("UNTRUSTED_COMMIT: commits touch sensitive paths and were not written by you\n\n")
	b.WriteString(fmt.Sprintf("  %-10s %-24s %s\n", "commit", "author", "sensitive paths"))
	for _, refusal := range decision.Refusals {
		author := refusal.Author
		if len(author) > 22 {
			author = author[:22]
		}
		b.WriteString(fmt.Sprintf("  %-10s %-24s %s\n", shortCommit(refusal.SHA), author, strings.Join(refusal.Paths, ", ")))
	}
	if len(decision.Refusals) > 0 {
		b.WriteString("\nAccept this commit with:\n")
		b.WriteString(fmt.Sprintf("  sila gate --trust-target %s --range %s\n", decision.Refusals[0].SHA, rangeExpr))
	}
	return b.String()
}

func shortCommit(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// showGateTrustConfig prints identity, targets, and the sensitive set.
func showGateTrustConfig(ui render.UI) {
	cwd, _ := os.Getwd()
	repoRoot := cwd
	if mgr, err := git.NewManager(cwd); err == nil {
		repoRoot = mgr.RepoRoot()
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · trust config")))
	fmt.Fprintf(os.Stdout, "  identity: %s\n", gitConfigEmail(repoRoot))
	targets := append([]string{}, gateTrust.Target)
	if cfg, err := config.Load(""); err == nil {
		targets = append(targets, cfg.Trust.Targets...)
	}
	fmt.Fprintf(os.Stdout, "  targets: %s\n", strings.Join(targets, ", "))
	fmt.Fprintf(os.Stdout, "  range: %s\n", strings.TrimSpace(gateTrust.Range))
	fmt.Fprintln(os.Stdout, "  sensitive:")
	for _, pattern := range gate.DefaultSensitivePatterns {
		fmt.Fprintf(os.Stdout, "    %s\n", pattern)
	}
}
