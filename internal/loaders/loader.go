package loaders

import (
	"errors"
	"fmt"
	"time"
)

// errMissingDB is returned when a source database/dir is not present.
var errMissingDB = fmt.Errorf("source data not found")

// IsMissingSource reports whether err means the source data is simply absent,
// which is normal on a machine without that harness.
func IsMissingSource(err error) bool { return errors.Is(err, errMissingDB) }

// Message is one knowledge-relevant normalized record emitted by a loader.
type Message struct {
	Tool       string    // canonical tool name, e.g. "devin", "mimocode"
	SessionID  string    // source session identifier (opaque)
	ProjectDir string    // normalized absolute working dir, "" if unknown
	TS         time.Time // message timestamp (UTC)
	Role       string    // "user" | "assistant" | tool-specific
	Content    string    // curated text
	// Source position, recorded in source_occurrences when available. These
	// are best-effort provenance hints; zero values are fine.
	SourceLocation string // file path, db path, or similar
	SourceOrder    int64  // position within the source (line/ordinal), 0 if unknown
}

// Loader ingests a single tool. The watermark is an opaque per-tool cursor
// ("" = full scan). It returns messages with TS strictly after the watermark
// and the new watermark to persist. project, when non-nil, restricts the scan
// to sessions whose working dir matches it (id/basename directory name).
type Loader interface {
	Name() string
	Load(watermark string, project *string) ([]Message, string, error)
	// Count returns the curated message total discoverable for the same
	// project filter Load uses for a full scan, without deserializing content.
	Count(project *string) (int64, error)
}
