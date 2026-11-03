package ingest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

// decide overlap tolerance: same project+role, within <window>, token-sim
// >= threshold, from a different tool.
const (
	overlapWindow    = 5 * time.Minute
	overlapThreshold = 0.5
)

// detectOverlaps leniently matches each newly written message against
// cross-tool messages in the same project/time window and records the overlap.
func detectOverlaps(st *store.Store, rows []store.LoadMessage) int {
	count := 0
	for _, r := range rows {
		t, err := time.Parse(time.RFC3339, r.TS)
		if err != nil {
			continue
		}
		lo := t.Add(-overlapWindow).UTC().Format(time.RFC3339)
		hi := t.Add(overlapWindow).UTC().Format(time.RFC3339)
		near, err := st.MessagesNear(r.ProjectID, r.Role, lo, hi)
		if err != nil {
			continue
		}
		m := dream.MatchOverlap(near, r.Content, r.Tool, overlapThreshold)
		if m == nil {
			continue
		}
		if err := st.RecordOverlap(store.Overlap{
			ProjectID:  r.ProjectID,
			MsgA:       r.ContentHash,
			MsgB:       m.ID,
			ToolsA:     []string{r.Tool},
			ToolsB:     m.Tools,
			Similarity: dream.Similarity(m.Content, r.Content),
		}); err == nil {
			count++
		}
	}
	return count
}

// Result summarizes one tool's ingest pass.
type Result struct {
	Tool       string
	Messages   int
	NewRecords int
	Merged     int
	Overlaps   int
	Error      error
}

// ToolProgress represents progress telemetry for an individual tool during ingest.
type ToolProgress struct {
	Tool       string
	Index      int
	Total      int
	Phase      string // "scanning", "scanned", "committing", "committed"
	Messages   int
	NewRecords int
	Merged     int
	Overlaps   int
	Error      error
}

// ProgressFunc is called when a tool progresses through scanning and committing.
type ProgressFunc func(p ToolProgress)

// Options controls a run.
type Options struct {
	Ctx        context.Context // optional context for graceful cancellation
	Tools      []string        // canonical names (empty = all)
	RunID      string          // "" = default store row
	ProjectSel string          // --project value
	Since      time.Time       // zero = all; only emit messages with TS >= Since
	Out        io.Writer       // human summary output
	Verbose    bool
	DryRun     bool         // preview planned ingestion without writing to database
	Full       bool         // ignore stored cursors and re-import all history
	OnProgress ProgressFunc // optional progress callback
}

// Run executes the ingest for the requested tools into st.
func Run(st *store.Store, opts Options) ([]Result, error) {
	return runWithSources(st, loaders.AllSources(), opts)
}

// loaded is the read-only result of one tool's load phase, ready to be committed.
type loaded struct {
	name  string
	wmKey string // watermark key; defaults to name, scoped for per-repo git
	msgs  []loaders.Message
	newWM string
	err   error
}

// runWithSources is Run with an injectable source map (used by tests to avoid
// scanning the dev machine's real tool databases). The expensive loader scans
// run concurrently (bounded pool), then commits are serialized to avoid SQLite
// write-lock contention.
func runWithSources(st *store.Store, sources map[string]*loaders.Source, opts Options) ([]Result, error) {
	if len(opts.Tools) == 0 {
		opts.Tools = loaders.SortedNames(sources)
	}

	ld := make([]loaded, len(opts.Tools))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, name := range opts.Tools {
		src, ok := sources[name]
		if !ok {
			continue
		}
		if opts.Ctx != nil && opts.Ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(i int, src *loaders.Source) {
			defer wg.Done()
			if opts.Ctx != nil {
				select {
				case <-opts.Ctx.Done():
					return
				case sem <- struct{}{}:
				}
			} else {
				sem <- struct{}{}
			}
			defer func() { <-sem }()
			if opts.Ctx != nil && opts.Ctx.Err() != nil {
				return
			}
			if opts.OnProgress != nil {
				opts.OnProgress(ToolProgress{
					Tool:  src.Name,
					Index: i + 1,
					Total: len(opts.Tools),
					Phase: "scanning",
				})
			}
			res := loadTool(st, src, opts)
			ld[i] = res
			if opts.OnProgress != nil {
				opts.OnProgress(ToolProgress{
					Tool:     src.Name,
					Index:    i + 1,
					Total:    len(opts.Tools),
					Phase:    "scanned",
					Messages: len(res.msgs),
					Error:    res.err,
				})
			}
		}(i, src)
	}
	wg.Wait()

	if opts.Ctx != nil && opts.Ctx.Err() != nil {
		return nil, opts.Ctx.Err()
	}

	var results []Result
	var missingSources []string
	for i, name := range opts.Tools {
		if _, ok := sources[name]; !ok {
			continue
		}
		if opts.Ctx != nil && opts.Ctx.Err() != nil {
			break
		}
		if opts.OnProgress != nil {
			opts.OnProgress(ToolProgress{
				Tool:     name,
				Index:    i + 1,
				Total:    len(opts.Tools),
				Phase:    "committing",
				Messages: len(ld[i].msgs),
			})
		}
		res := commitTool(st, ld[i], opts)
		results = append(results, res)
		if opts.Out != nil && res.Error != nil {
			if loaders.IsMissingSource(res.Error) {
				missingSources = append(missingSources, name)
			} else {
				fmt.Fprintf(opts.Out, "%s: %v\n", name, res.Error)
			}
		}
		if opts.OnProgress != nil {
			opts.OnProgress(ToolProgress{
				Tool:       name,
				Index:      i + 1,
				Total:      len(opts.Tools),
				Phase:      "committed",
				Messages:   res.Messages,
				NewRecords: res.NewRecords,
				Merged:     res.Merged,
				Overlaps:   res.Overlaps,
				Error:      res.Error,
			})
		}
	}
	if opts.Out != nil && len(missingSources) > 0 {
		if opts.Verbose {
			for _, name := range missingSources {
				fmt.Fprintf(opts.Out, "%s: source data not found\n", name)
			}
		} else {
			fmt.Fprintf(opts.Out, "%d harness sources not found: %s (run 'sila sync --verbose' to list)\n", len(missingSources), strings.Join(missingSources, ", "))
		}
	}
	return results, nil
}

// loadTool runs the read-only loader scan for one source.
func loadTool(st *store.Store, src *loaders.Source, opts Options) loaded {
	ld := loaded{name: src.Name, wmKey: src.Name}
	loader, err := loaders.BuildLoader(src)
	if err != nil {
		ld.err = err
		return ld
	}
	var projSel *string
	if opts.ProjectSel != "" {
		s := opts.ProjectSel
		projSel = &s
	}
	// Git is repository-scoped: the loader needs a directory, not a project
	// selector, and its cursor is tracked per repository so a sync in one
	// workspace cannot skip commits in another.
	if src.Name == "git" {
		repoDir, wmKey := st.GitScope(opts.ProjectSel)
		ld.wmKey = wmKey
		projSel = &repoDir
	}
	wm, err := st.GetWatermark(opts.RunID, ld.wmKey)
	if err != nil {
		ld.err = err
		return ld
	}
	if opts.Full {
		// A full run re-reads every source record, which backfills source
		// occurrences for history ingested before occurrence tracking existed.
		// Exact content dedup keeps the message rows themselves unchanged.
		wm = ""
	}
	msgs, newWM, err := loader.Load(wm, projSel)
	if err != nil {
		ld.err = err
		return ld
	}
	if !opts.Since.IsZero() {
		kept := msgs[:0]
		for _, m := range msgs {
			if !m.TS.Before(opts.Since) {
				kept = append(kept, m)
			}
		}
		msgs = kept
	}
	ld.msgs, ld.newWM = msgs, newWM
	return ld
}

// commitTool writes a tool's loaded messages into the store (serialized).
func commitTool(st *store.Store, ld loaded, opts Options) Result {
	res := Result{Tool: ld.name}
	if ld.err != nil {
		res.Error = ld.err
		return res
	}
	res.Messages = len(ld.msgs)
	rows, err := normalize(st, ld.name, ld.msgs, opts.ProjectSel, opts.DryRun)
	if err != nil {
		res.Error = err
		return res
	}
	wmToCommit := ld.newWM
	if opts.ProjectSel != "" && !strings.HasPrefix(ld.wmKey, "git:") {
		// When scoped to a single project, project-scoped tools only inspected
		// a subset of sessions. Do not advance the global tool watermark.
		// Git is the exception: a scoped run inspected the whole repository,
		// so its per-repository cursor advances safely.
		wmToCommit = ""
	}
	if opts.DryRun {
		res.NewRecords = len(rows)
		return res
	}

	newRecords, merged, err := st.WriteRunScoped(opts.RunID, ld.name, ld.wmKey, wmToCommit, rows)
	if err != nil {
		res.Error = err
		return res
	}
	res.NewRecords = newRecords
	res.Merged = merged
	if newRecords > 0 {
		res.Overlaps = detectOverlaps(st, rows)
	}
	return res
}

// normalize resolves each message's project id and content hash, falling back
// to a synthetic project for messages without a working dir. When projectSel is
// set, dirs that don't match it are dropped at load time by the loaders.
func normalize(st *store.Store, tool string, msgs []loaders.Message, projectSel string, dryRun bool) ([]store.LoadMessage, error) {
	rows := make([]store.LoadMessage, 0, len(msgs))
	seen := map[string]string{} // projectDir -> id
	for _, m := range msgs {
		if projectSel != "" && m.ProjectDir != "" && !dirMatches(m.ProjectDir, projectSel) {
			continue
		}
		projPath := m.ProjectDir
		if projPath == "" {
			projPath = filepath.Join(os.Getenv("HOME"), "sila", "no-project")
		} else {
			projPath = store.CanonicalProjectPath(projPath)
		}
		id, ok := seen[projPath]
		if !ok {
			if dryRun {
				if p, okP, _ := st.ProjectForDir(projPath); okP {
					id = p.ID
				} else {
					id = store.ProjectID(projPath)
				}
			} else {
				p, err := st.EnsureProject(projPath)
				if err != nil {
					return nil, err
				}
				id = p.ID
			}
			seen[projPath] = id
		}
		rows = append(rows, store.LoadMessage{
			ProjectID:      id,
			ContentHash:    store.ContentHash(m.Role, m.Content),
			TS:             m.TS.UTC().Format(time.RFC3339),
			Role:           m.Role,
			Content:        m.Content,
			Tool:           tool,
			SessionID:      m.SessionID,
			SourceLocation: m.SourceLocation,
			SourceOrder:    m.SourceOrder,
			SourceTime:     store.SortableTime(m.TS),
		})
	}
	return rows, nil
}

// dirMatches reports whether dir's basename (dir name) equals sel, or dir
// equals sel. This is the project-id/dir-name matching semantics.
func dirMatches(dir, sel string) bool {
	dir = store.CanonicalProjectPath(dir)
	sel = store.CanonicalProjectPath(sel)
	if filepath.Clean(dir) == filepath.Clean(sel) {
		return true
	}
	if filepath.Base(filepath.Clean(dir)) == sel {
		return true
	}
	return strings.EqualFold(filepath.Base(filepath.Clean(dir)), sel)
}
