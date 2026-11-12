package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// findCommitReceipt resolves a full or short SHA within a project.
func findCommitReceipt(st *store.Store, projectID, sha string) (*store.CommitReceipt, bool, []string, error) {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return nil, false, nil, nil
	}
	receipts, err := st.ListCommitReceipts(projectID, 200)
	if err != nil {
		return nil, false, nil, err
	}
	var matches []store.CommitReceipt
	for _, receipt := range receipts {
		if strings.HasPrefix(receipt.CommitSHA, sha) {
			matches = append(matches, receipt)
		}
	}
	if len(matches) == 0 {
		return nil, true, nil, nil
	}
	if len(matches) > 1 {
		candidates := make([]string, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, match.CommitSHA)
		}
		return nil, false, candidates, nil
	}
	return &matches[0], false, nil, nil
}

func runCommits(ui render.UI, dbPath, project, sinceFlag string, jsonOut bool, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila commits: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	cwd, _ := os.Getwd()
	projectID := project
	if projectID == "" || projectID == "all" {
		projectID = store.ProjectID(cwd)
	}
	if len(args) > 0 && (args[0] == "show" || args[0] == "get") {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sila commits show: usage: sila commits show <sha>")
			os.Exit(2)
		}
		receipt, notFound, candidates, err := findCommitReceipt(st, projectID, args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila commits: %v\n", err)
			os.Exit(1)
		}
		if notFound {
			fmt.Fprintln(os.Stderr, "commit receipt not found")
			os.Exit(1)
		}
		if len(candidates) > 1 {
			fmt.Fprintf(os.Stderr, "sila commits: ambiguous short sha %q: %s\n", args[1], strings.Join(candidates, ", "))
			os.Exit(2)
		}
		if jsonOut {
			emitJSON(receipt)
			return
		}
		printCommitReceipt(ui, *receipt)
		return
	}
	since := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if strings.TrimSpace(sinceFlag) != "" {
		parsed, _, parseErr := parseActivitySince(sinceFlag)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "sila commits: %v\n", parseErr)
			os.Exit(2)
		}
		since = parsed
	}
	receipts, err := st.ListCommitReceipts(projectID, 200)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila commits: %v\n", err)
		os.Exit(1)
	}
	var filtered []store.CommitReceipt
	for _, receipt := range receipts {
		if receipt.CreatedAt.IsZero() || receipt.CreatedAt.After(since) || receipt.CreatedAt.Equal(since) {
			filtered = append(filtered, receipt)
		}
	}
	if jsonOut {
		emitJSON(filtered)
		return
	}
	if len(filtered) == 0 {
		fmt.Fprintln(os.Stdout, "no commit receipts in this window")
		return
	}
	rows := make([][]string, 0, len(filtered))
	for _, receipt := range filtered {
		rows = append(rows, []string{shortSHA(receipt.CommitSHA), receipt.SessionID, receipt.Agent, receipt.Source, receipt.CreatedAt.Format("2006-01-02 15:04")})
	}
	fmt.Fprint(os.Stdout, ui.Table([]string{"commit", "session", "agent", "source", "created"}, rows, []string{"left", "left", "left", "left", "left"}))
}

func printCommitReceipt(ui render.UI, receipt store.CommitReceipt) {
	fmt.Fprintf(os.Stdout, "%s %s\n", ui.Bold(receipt.CommitSHA), ui.Dim(receipt.Source))
	fmt.Fprintf(os.Stdout, "  session: %s\n  agent: %s\n  harness: %s\n  branch: %s\n  tree: %s\n  created: %s\n",
		receipt.SessionID, receipt.Agent, receipt.Harness, receipt.Branch, receipt.TreeHash, receipt.CreatedAt.Format(time.RFC3339))
}
