package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/importmem"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

const continuityBundleSchema = "sila/continuity-bundle/v1"

type continuityBundleManifest struct {
	Schema      string            `json:"schema"`
	GeneratedAt string            `json:"generated_at"`
	Counts      map[string]int    `json:"counts"`
	Files       map[string]string `json:"files"`
}

type bundleRecord struct {
	ID        string
	ProjectID string
	Kind      string
	Title     string
	Body      string
	CreatedAt string
}

// runExportContinuity writes the portable continuity bundle.
func runExportContinuity(ui render.UI, dbPath, project, outDir string, force, jsonOut bool) {
	outDir = strings.TrimSpace(outDir)
	if outDir == "" {
		fmt.Fprintln(os.Stderr, "sila export continuity: --out=<dir> is required")
		os.Exit(2)
	}
	if continuityDestRefused(outDir, force) {
		fmt.Fprintf(os.Stderr, "sila export continuity: %s is not empty (use --force)\n", outDir)
		os.Exit(1)
	}
	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila export continuity: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	projectID := project
	if projectID == "" || projectID == "all" {
		projectID = ""
	}
	records, goals, err := collectContinuityBundle(st, projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila export continuity: %v\n", err)
		os.Exit(1)
	}
	parent := filepath.Dir(outDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "sila export continuity: %v\n", err)
		os.Exit(1)
	}
	tmp, err := os.MkdirTemp(parent, ".continuity-bundle-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila export continuity: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	manifest, err := writeContinuityBundle(tmp, records, goals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila export continuity: %v\n", err)
		os.Exit(1)
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "sila export continuity: %v\n", err)
		os.Exit(1)
	}
	if force {
		_ = os.RemoveAll(outDir)
	}
	if err := os.Rename(tmp, outDir); err != nil {
		fmt.Fprintf(os.Stderr, "sila export continuity: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(manifest)
		return
	}
	fmt.Fprintf(os.Stdout, "wrote continuity bundle to %s (%d handoffs, %d cards, %d memories, %d goals)\n",
		outDir, manifest.Counts["handoffs"], manifest.Counts["cards"], manifest.Counts["memories"], manifest.Counts["goals"])
}

// continuityDestRefused reports whether --force is required for this destination.
func continuityDestRefused(dir string, force bool) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0 && !force
}

func collectContinuityBundle(st *store.Store, projectID string) ([]bundleRecord, []bundleRecord, error) {
	var records []bundleRecord
	projectFilter := ""
	args := []any{}
	if projectID != "" {
		projectFilter = " AND project_id = ?"
		args = append(args, projectID)
	}
	rows, err := st.DB.Query(`SELECT id, project_id, agent, summary, created_at FROM handoffs WHERE 1=1`+projectFilter+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var rec bundleRecord
		var agent, summary, created string
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &agent, &summary, &created); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rec.Kind = "handoff"
		rec.Title = "Handoff " + rec.ID
		rec.CreatedAt = created
		rec.Body = fmt.Sprintf("# Handoff %s\n\nAgent: %s\nCreated: %s\n\n%s\n", rec.ID, agent, created, summary)
		records = append(records, rec)
	}
	rows.Close()

	rows, err = st.DB.Query(`SELECT id, project_id, session_id, agent, payload_json, created_at FROM session_cards WHERE 1=1`+strings.Replace(projectFilter, "project_id", "project_id", 1)+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var rec bundleRecord
		var sessionID, agent, payload, created string
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &sessionID, &agent, &payload, &created); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rec.Kind = "card"
		rec.Title = "Session card " + sessionID
		rec.CreatedAt = created
		rec.Body = fmt.Sprintf("# Session card %s\n\nAgent: %s\nCreated: %s\n\n```json\n%s\n```\n", sessionID, agent, created, payload)
		records = append(records, rec)
	}
	rows.Close()

	rows, err = st.DB.Query(`SELECT id, project_id, kind, title, summary, created_at FROM memory_records WHERE state = 'current'`+projectFilter+` ORDER BY kind, id`, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var rec bundleRecord
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &rec.Kind, &rec.Title, &rec.Body, &rec.CreatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rec.Body = fmt.Sprintf("# %s (%s)\n\n%s\n", rec.Title, rec.Kind, rec.Body)
		records = append(records, rec)
	}
	rows.Close()

	goalArgs := []any{}
	goalFilter := ""
	if projectID != "" {
		goalFilter = " WHERE project_id = ?"
		goalArgs = append(goalArgs, projectID)
	}
	rows, err = st.DB.Query(`SELECT goal_slug, commit_sha, ccdsi_score, certified_at FROM goal_audit_receipts`+goalFilter+` ORDER BY goal_slug`, goalArgs...)
	if err != nil {
		return nil, nil, err
	}
	var goals []bundleRecord
	for rows.Next() {
		var slug, sha, certified string
		var score float64
		if err := rows.Scan(&slug, &sha, &score, &certified); err != nil {
			rows.Close()
			return nil, nil, err
		}
		goals = append(goals, bundleRecord{ID: slug, Kind: "goal", Title: slug, Body: fmt.Sprintf("%s\t%s\t%.2f\t%s", slug, shortSHA(sha), score, certified), CreatedAt: certified})
	}
	rows.Close()
	sort.Slice(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		return records[i].ID < records[j].ID
	})
	return records, goals, nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func writeContinuityBundle(dir string, records, goals []bundleRecord) (continuityBundleManifest, error) {
	manifest := continuityBundleManifest{Schema: continuityBundleSchema, Counts: map[string]int{}, Files: map[string]string{}}
	generated := time.Now().UTC()
	if pinned := strings.TrimSpace(os.Getenv("SILA_BUNDLE_TIME")); pinned != "" {
		if parsed, err := time.Parse(time.RFC3339, pinned); err == nil {
			generated = parsed.UTC()
		}
	}
	manifest.GeneratedAt = generated.Format(time.RFC3339)
	files := map[string]string{}
	subdirs := map[string]string{"handoff": "handoffs", "card": "cards", "goal": "goals"}
	for _, rec := range records {
		sub := subdirs[rec.Kind]
		if sub == "" {
			sub = "memories"
		}
		if rec.Kind == "memo" || rec.Kind == "lesson" || rec.Kind == "case" {
			sub = "memories"
		}
		name := "memories"
		switch rec.Kind {
		case "handoff":
			name = "handoffs"
		case "card":
			name = "cards"
		}
		rel := filepath.ToSlash(filepath.Join(name, safeBundleName(rec.ID)+".md"))
		files[rel] = rec.Body
	}
	var goalLines []string
	goalLines = append(goalLines, "# Goals", "", "| slug | commit | ccdsi | certified |", "|------|--------|-------|-----------|")
	for _, goal := range goals {
		parts := strings.Split(goal.Body, "\t")
		if len(parts) == 4 {
			goalLines = append(goalLines, fmt.Sprintf("| %s | %s | %s | %s |", parts[0], parts[1], parts[2], parts[3]))
		}
	}
	files["goals.md"] = strings.Join(goalLines, "\n") + "\n"
	files["README.md"] = fmt.Sprintf("# Sila continuity bundle\n\nSchema: %s\nGenerated: %s\n\nThis bundle is redacted and deterministic. Handoffs, session cards, memories, and goals are grouped in their own directories. Verify every file against manifest.json.\n",
		continuityBundleSchema, manifest.GeneratedAt)

	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, rel := range keys {
		redacted, err := importmem.Redact(files[rel], dir)
		if err != nil {
			return manifest, err
		}
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return manifest, err
		}
		if err := os.WriteFile(full, []byte(redacted.Text), 0o600); err != nil {
			return manifest, err
		}
		sum := sha256.Sum256([]byte(redacted.Text))
		manifest.Files[rel] = "sha256:" + hex.EncodeToString(sum[:])
	}
	for _, rec := range records {
		switch rec.Kind {
		case "handoff":
			manifest.Counts["handoffs"]++
		case "card":
			manifest.Counts["cards"]++
		default:
			manifest.Counts["memories"]++
		}
	}
	manifest.Counts["goals"] = len(goals)
	manifest.Counts["files"] = len(files) + 1
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(encoded, '\n'), 0o600); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func safeBundleName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "record"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "-", " ", "_", "..", "_")
	return replacer.Replace(id)
}
