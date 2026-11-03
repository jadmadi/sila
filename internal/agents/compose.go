package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// ManagedBlockMeta is the parsed fingerprint of a sila-managed block. Revision
// is the block schema revision, Scope identifies the owning pack, and Hash is
// the deterministic fingerprint of the managed body.
type ManagedBlockMeta struct {
	Revision int    `json:"revision"`
	Scope    string `json:"scope"`
	Hash     string `json:"hash"`
}

// FormatManagedBlockMeta renders the single owned metadata line that follows
// OwnedBlockBegin. The line is sila-owned content: it is stripped from
// preserved user content on every merge.
func FormatManagedBlockMeta(meta ManagedBlockMeta) string {
	return fmt.Sprintf("%srev=%d scope=%s hash=%s%s", OwnedBlockMetaPrefix, meta.Revision, meta.Scope, meta.Hash, OwnedBlockMetaSuffix)
}

// ParseManagedBlockMeta parses a metadata line. Malformed revisions or hashes
// and unknown scopes return false so callers treat the block as legacy rather
// than as user content.
func ParseManagedBlockMeta(line string) (ManagedBlockMeta, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, OwnedBlockMetaPrefix) || !strings.HasSuffix(trimmed, OwnedBlockMetaSuffix) {
		return ManagedBlockMeta{}, false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(trimmed, OwnedBlockMetaPrefix), OwnedBlockMetaSuffix)

	var meta ManagedBlockMeta
	for _, field := range strings.Fields(inner) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return ManagedBlockMeta{}, false
		}
		switch key {
		case "rev":
			rev, err := strconv.Atoi(value)
			if err != nil || rev < 1 {
				return ManagedBlockMeta{}, false
			}
			meta.Revision = rev
		case "scope":
			meta.Scope = value
		case "hash":
			meta.Hash = value
		}
	}
	if meta.Revision < 1 || meta.Scope != ManagedScopeCore || !isManagedHash(meta.Hash) {
		return ManagedBlockMeta{}, false
	}
	return meta, true
}

// isManagedHash reports whether s is a 16-character lowercase hex fingerprint.
func isManagedHash(s string) bool {
	if len(s) != 16 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// HashManagedBody returns the deterministic fingerprint of a compiled managed
// body: the first 16 lowercase hex characters of SHA-256. CRLF and bare CR are
// normalized to LF, exactly one trailing newline is removed, and the leading
// per-project H1 is excluded so identical rule sets fingerprint identically
// across projects. Leading whitespace and trailing spaces on content lines are
// preserved: they are content, not formatting noise.
func HashManagedBody(body string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	if strings.HasPrefix(normalized, "# ") {
		if idx := strings.IndexByte(normalized, '\n'); idx >= 0 {
			normalized = strings.TrimLeft(normalized[idx+1:], "\n")
		} else {
			normalized = ""
		}
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])[:16]
}

// ExtractManagedBlock returns the body, metadata, and status of the first
// well-ordered managed block in content. ok reports whether a begin/end pair
// exists; legacy reports that the pair has no valid metadata line (an older
// sila wrote it) and therefore needs a one-time migration.
func ExtractManagedBlock(content string) (body string, meta ManagedBlockMeta, ok bool, legacy bool) {
	begin := strings.Index(content, OwnedBlockBegin)
	if begin < 0 {
		return "", ManagedBlockMeta{}, false, false
	}
	rest := content[begin+len(OwnedBlockBegin):]
	end := strings.Index(rest, OwnedBlockEnd)
	if end < 0 {
		return "", ManagedBlockMeta{}, false, false
	}

	inner := rest[:end]
	inner = strings.TrimPrefix(inner, "\r\n")
	inner = strings.TrimPrefix(inner, "\n")
	if strings.HasSuffix(inner, "\r\n") {
		inner = inner[:len(inner)-2]
	} else if strings.HasSuffix(inner, "\n") {
		inner = inner[:len(inner)-1]
	}
	inner = strings.ReplaceAll(inner, "\r\n", "\n")
	lines := strings.Split(inner, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), OwnedBlockMetaPrefix) {
		if parsed, valid := ParseManagedBlockMeta(lines[0]); valid {
			meta = parsed
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n"), meta, true, meta.Hash == ""
}

// stripLeadingH1 removes a leading Markdown H1 line so composed rules keep a
// single title.
func stripLeadingH1(s string) string {
	trimmed := strings.TrimLeft(s, "\n")
	if !strings.HasPrefix(trimmed, "# ") {
		return s
	}
	if _, after, ok := strings.Cut(trimmed, "\n"); ok {
		return strings.TrimLeft(after, "\n")
	}
	return ""
}

// composeManagedBody resolves the body written inside the owned block. When the
// user's file already carries a title, the managed body's H1 is dropped so the
// document keeps a single heading.
func composeManagedBody(userContent, managed string) string {
	body := managed
	if hasLeadingH1(userContent) {
		body = stripLeadingH1(body)
	}
	return strings.TrimRight(body, "\n")
}

// ComposeAgentsFile assembles the full AGENTS.md content: existing user
// content (everything outside the owned block) is preserved verbatim, and the
// sila-managed block is regenerated around the compiled body with exactly one
// metadata fingerprint line.
func ComposeAgentsFile(existing, managed string) string {
	userContent := ExtractUserContent(existing)
	body := composeManagedBody(userContent, managed)
	meta := ManagedBlockMeta{
		Revision: ManagedRevision,
		Scope:    ManagedScopeCore,
		Hash:     HashManagedBody(body),
	}

	var sb strings.Builder
	if userContent != "" {
		sb.WriteString(strings.TrimRight(userContent, "\n"))
		sb.WriteString("\n\n")
	}
	sb.WriteString(OwnedBlockBegin + "\n")
	sb.WriteString(FormatManagedBlockMeta(meta) + "\n")
	sb.WriteString(body + "\n")
	sb.WriteString(OwnedBlockEnd + "\n")
	return sb.String()
}

// hasLeadingH1 reports whether the first non-empty line is a Markdown H1.
func hasLeadingH1(s string) bool {
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, "# ")
	}
	return false
}

// ExtractUserContent returns the portion of an existing instruction file that
// sila does not own: everything outside a well-ordered sila:begin/end marker
// pair. A file with no such pair is treated entirely as user content and never
// discarded — a familiar heading is not proof that sila wrote any of it.
func ExtractUserContent(existing string) string {
	if existing == "" {
		return ""
	}
	begin := strings.Index(existing, OwnedBlockBegin)
	end := strings.Index(existing, OwnedBlockEnd)
	if begin >= 0 && end > begin {
		var parts []string
		if before := strings.TrimRight(stripOwnedMetadata(existing[:begin]), "\n"); strings.TrimSpace(before) != "" {
			parts = append(parts, before)
		}
		if after := strings.TrimSpace(stripOwnedMetadata(existing[end+len(OwnedBlockEnd):])); after != "" {
			parts = append(parts, after)
		}
		return strings.Join(parts, "\n\n")
	}
	// No well-ordered marker pair: ownership is unknown, and a familiar heading
	// does not prove sila wrote any part of the file. The whole file is
	// preserved as user content, with sila's own stray marker lines removed so
	// the rewrite converges instead of appending a fresh block every run.
	// (Files written by very old sila versions keep their old generated prefix
	// this way — duplicated once under the new block — which is the safe
	// direction: preservation over dedup.)
	return strings.TrimRight(stripOwnedMarkers(existing), "\n")
}

// stripOwnedMetadata removes sila metadata lines from content that is being
// preserved as user-owned. The metadata line is always sila-owned, even when
// it appears outside a well-ordered marker pair.
func stripOwnedMetadata(content string) string {
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), OwnedBlockMetaPrefix) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// stripOwnedMarkers removes sila's marker and metadata comments from content
// that is being preserved as user-owned, including lines whose marker text has
// drifted.
func stripOwnedMarkers(content string) string {
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "<!-- sila:begin") || trimmed == OwnedBlockEnd ||
			strings.HasPrefix(trimmed, OwnedBlockMetaPrefix) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}
