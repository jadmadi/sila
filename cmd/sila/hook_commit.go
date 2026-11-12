package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/proclive"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// gitCommitInfo reads HEAD, branch, and tree without touching the commit.
func gitCommitInfo(repoDir string) (sha, branch, tree string, err error) {
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD", "HEAD^{tree}").Output()
	if err != nil {
		return "", "", "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return "", "", "", fmt.Errorf("unexpected rev-parse output")
	}
	sha = strings.TrimSpace(lines[0])
	tree = strings.TrimSpace(lines[1])
	if branchOut, branchErr := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output(); branchErr == nil {
		branch = strings.TrimSpace(string(branchOut))
		if branch == "HEAD" {
			branch = ""
		}
	}
	return sha, branch, tree, nil
}

// resolveCommitReceipt binds HEAD to the nearest eligible session ancestor.
func resolveCommitReceipt(st *store.Store, projectID, repoDir string) (*store.CommitReceipt, bool, error) {
	sha, branch, tree, err := gitCommitInfo(repoDir)
	if err != nil {
		return nil, false, err
	}
	chain, err := proclive.Ancestry(os.Getpid(), 0)
	if err != nil {
		return nil, false, err
	}
	pidDepth := map[int]int{}
	tickDepth := map[int64]int{}
	for i, ancestor := range chain {
		pidDepth[ancestor.PID] = i
		if ancestor.StartTicks != 0 {
			if _, seen := tickDepth[ancestor.StartTicks]; !seen {
				tickDepth[ancestor.StartTicks] = i
			}
		}
	}
	sessions, err := st.ListAgentSessions(projectID, time.Now().Add(-24*time.Hour), 200)
	if err != nil {
		return nil, false, err
	}
	var best *store.AgentSession
	bestDepth := int(^uint(0) >> 1)
	for i := range sessions {
		session := &sessions[i]
		if session.PIDStartTicks == 0 || !session.ClosedAt.IsZero() {
			continue
		}
		d, ok := 0, false
		if session.PID > 0 {
			if pd, pidOK := pidDepth[session.PID]; pidOK && chain[pd].StartTicks == session.PIDStartTicks {
				d, ok = pd, true
			}
		}
		if !ok {
			d, ok = tickDepth[session.PIDStartTicks]
		}
		if !ok {
			continue
		}
		if d < bestDepth {
			best, bestDepth = session, d
		}
	}
	if best == nil {
		return nil, false, nil
	}
	return &store.CommitReceipt{
		CommitSHA:   sha,
		ProjectID:   projectID,
		SessionID:   best.SessionID,
		Agent:       best.Agent,
		Harness:     best.Harness,
		Branch:      branch,
		TreeHash:    tree,
		Source:      "ancestry",
		AncestorPID: best.PID,
		CreatedAt:   time.Now().UTC(),
	}, true, nil
}

// runHookCommit writes a receipt or warns once. It never fails the commit.
func runHookCommit(ui render.UI, dbPath, project, cwd string, jsonOut bool) {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	projectID := project
	if projectID == "" {
		projectID = store.ProjectID(cwd)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hook commit: %v\n", err)
		return
	}
	defer st.Close()
	receipt, found, err := resolveCommitReceipt(st, projectID, cwd)
	if err != nil {
		if errors.Is(err, proclive.ErrAncestryUnavailable) {
			fmt.Fprintln(os.Stderr, "sila hook commit: process ancestry is unavailable on this platform; receipt skipped")
			return
		}
		fmt.Fprintf(os.Stderr, "sila hook commit: %v\n", err)
		return
	}
	if !found {
		return
	}
	if err := st.SaveCommitReceipt(*receipt); err != nil {
		fmt.Fprintf(os.Stderr, "sila hook commit: %v\n", err)
		return
	}
	if jsonOut {
		emitJSON(receipt)
	}
}
