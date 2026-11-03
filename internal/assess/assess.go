package assess

import (
	"os"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

// Options controls an assess run.
type Options struct {
	Tools      []string // canonical names (empty = all)
	ProjectSel string
}

// projectScopedTools lists loaders whose source carries a working directory, so
// --project actually scopes them. The tools that carry no cwd (codex,
// command-code, codewhale, agy) ignore project scoping; under --project they're
// reported as n/a rather than showing misleading global totals.
var projectScopedTools = map[string]bool{
	"mimocode": true, "opencode": true, "devin": true,
	"zcode": true, "claude-code": true, "muse": true,
}

// Report is one tool's discovery result. It never writes; it only opens
// sources read-only and reads the store watermark.
type Report struct {
	Tool         string
	Source       string
	Exists       bool
	Size         int64
	ModTime      string // RFC3339, "" if unstatable
	HasWatermark bool
	Total        int64 // curated messages discoverable (full scan)
	New          int64 // would be ingested on the next run (past the watermark)
	NotScoped    bool  // project request did not apply to this tool
	Error        error
}

// Run collects a discovery report for each requested tool. st may be nil to
// indicate no store exists yet (treats every tool as having no watermark).
func Run(st *store.Store, opts Options) ([]Report, error) {
	return runWithSources(st, loaders.AllSources(), opts)
}

// runWithSources is Run with an injectable source map (test hermeticity). The
// expensive per-tool load phase (loader scans) runs concurrently in a bounded
// pool, then reports are collected serially — mirroring ingest's runWithSources.
func runWithSources(st *store.Store, sources map[string]*loaders.Source, opts Options) ([]Report, error) {
	if len(opts.Tools) == 0 {
		opts.Tools = loaders.SortedNames(sources)
	}

	reports := make([]Report, len(opts.Tools))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, name := range opts.Tools {
		src, ok := sources[name]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(i int, src *loaders.Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			reports[i] = assessTool(st, src, opts.ProjectSel)
		}(i, src)
	}
	wg.Wait()

	out := make([]Report, 0, len(opts.Tools))
	for i, name := range opts.Tools {
		if _, ok := sources[name]; !ok {
			continue
		}
		out = append(out, reports[i])
	}
	return out, nil
}

func assessTool(st *store.Store, src *loaders.Source, projectSel string) Report {
	rep := Report{Tool: src.Name}

	// Git resolves a project selector to its repository directory and tracks
	// one cursor per repository, so it is scoped (and never n/a) under
	// --project.
	wmKey := src.Name
	if src.Name == "git" {
		switch {
		case st != nil:
			projectSel, wmKey = st.GitScope(projectSel)
		case projectSel != "":
			wmKey = "git:" + store.ProjectID(projectSel)
		default:
			if wd, err := os.Getwd(); err == nil {
				projectSel = wd
				wmKey = "git:" + store.ProjectID(wd)
			}
		}
	}

	// Under a project scope, sources without a working directory can't be
	// narrowed; report them as n/a instead of showing global totals.
	if src.Name != "git" && projectSel != "" && !projectScopedTools[src.Name] {
		rep.Source = sourcePath(src)
		rep.NotScoped = true
		if p, ok := src.ResolvePath(); ok {
			rep.Exists = true
			rep.Source = p
		} else {
			rep.Exists = false
			rep.Source = sourcePath(src)
		}
		return rep
	}

	p, ok := src.ResolvePath()
	if !ok {
		rep.Exists = false
		rep.Source = sourcePath(src)
		return rep
	}
	rep.Exists = true
	rep.Source = p
	if fi, err := os.Stat(p); err == nil {
		rep.Size = fi.Size()
		rep.ModTime = fi.ModTime().UTC().Format(time.RFC3339)
	}

	loader, err := loaders.BuildLoader(src)
	if err != nil {
		rep.Error = err
		return rep
	}
	var projSel *string
	if projectSel != "" {
		projSel = &projectSel
	}

	// total curated messages discoverable — a COUNT, no content deserialization
	// (avoids the second full scan that a Load("") would perform).
	total, err := loader.Count(projSel)
	if err != nil {
		rep.Error = err
		return rep
	}
	rep.Total = total

	// would-ingest: what the next run pulls past the stored watermark
	wm, _ := stWatermark(st, wmKey)
	if wm != "" {
		rep.HasWatermark = true
	}
	newMsgs, _, err := loader.Load(wm, projSel)
	if err != nil {
		rep.Error = err
		return rep
	}
	// Cursors re-emit the record sitting at the watermark boundary, and that
	// record is normally already stored — counting it as "new" would report a
	// permanent backlog that never drains. Subtract what the store already has.
	rep.New = int64(len(newMsgs))
	if st != nil && len(newMsgs) > 0 {
		hashes := make([]string, 0, len(newMsgs))
		for _, m := range newMsgs {
			hashes = append(hashes, store.ContentHash(m.Role, m.Content))
		}
		if existing, herr := st.ExistingContentHashes(hashes); herr == nil {
			fresh := int64(0)
			for _, h := range hashes {
				if _, ok := existing[h]; !ok {
					fresh++
				}
			}
			rep.New = fresh
		}
	}
	return rep
}

func stWatermark(st *store.Store, tool string) (string, error) {
	if st == nil {
		return "", nil
	}
	return st.GetWatermark("", tool)
}

func sourcePath(src *loaders.Source) string {
	switch {
	case src.DBPath != "":
		return src.DBPath
	case src.File != "":
		return src.File
	default:
		return src.DataDir
	}
}
