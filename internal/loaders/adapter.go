package loaders

// funcLoader adapts a bare ingest func into the Loader interface for
// path-scanned tools (Codex/command-code/codewhale/Agy). wm is the stored
// per-tool cursor threads through to the underlying ingest func.
type funcLoader struct {
	name  string
	path  string
	fn    func(path string, project *string, wm string) ([]Message, string, error)
	count func(path string, project *string) (int64, error)
}

// Name returns the adapted loader identifier.
func (f funcLoader) Name() string { return f.name }

// Load invokes the underlying ingest function with watermark and project scope.
func (f funcLoader) Load(wm string, p *string) ([]Message, string, error) {
	return f.fn(f.path, p, wm)
}

// Count returns the number of candidate items for this loader.
func (f funcLoader) Count(p *string) (int64, error) {
	return f.count(f.path, p)
}

// firstNonEmptyString returns the first non-empty value, for provenance fields
// that a source records in more than one place.
func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
