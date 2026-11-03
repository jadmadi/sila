package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Canonical output glyphs. Color is separate: --no-color changes bytes only by
// removing ANSI codes, never by substituting a different glyph.
const (
	GlyphSuccess = "✔"
	GlyphFailure = "✖"
	GlyphWarning = "▲"
	GlyphDetail  = "•"
	GlyphHint    = "→"
	GlyphInfo    = "ℹ"
)

// Field is one labeled value in a success card.
type Field struct {
	Label string
	Value string
}

// Card is the single success shape emitted by mutating commands:
//
//	✔ <Verb> <Object> <ID>
//	  Label: value
type Card struct {
	Verb   string
	Object string
	ID     string
	Fields []Field
}

// Header describes one human command header.
type Header struct {
	Command string
	Scope   string
	Title   string
	Mode    string
}

// RenderHeader renders one of the four documented header forms:
//
//	sila · cmd · scope — title
//	sila · cmd — title
//	sila · cmd · scope
//	sila · cmd
func RenderHeader(ui UI, h Header) string {
	var b strings.Builder
	b.WriteString(ui.Cyan(ui.Bold("sila · " + strings.TrimSpace(h.Command))))
	if scope := strings.TrimSpace(h.Scope); scope != "" {
		b.WriteString(" · " + ui.Magenta(ui.Bold(scope)))
	}
	if title := strings.TrimSpace(h.Title); title != "" {
		b.WriteString(" — " + ui.Bold(title))
	}
	if mode := strings.TrimSpace(h.Mode); mode != "" {
		b.WriteString(ui.Dim(" (" + mode + ")"))
	}
	b.WriteString("\n")
	return b.String()
}

// RenderCard renders one success card. Empty list values should be passed as
// "none" by the caller; labels are aligned on the longest label.
func RenderCard(ui UI, c Card) string {
	var b strings.Builder
	b.WriteString(ui.Green(GlyphSuccess))
	parts := []string{strings.TrimSpace(c.Verb), strings.TrimSpace(c.Object), strings.TrimSpace(c.ID)}
	for _, p := range parts {
		if p != "" {
			b.WriteString(" " + ui.Bold(p))
		}
	}
	b.WriteString("\n")
	width := 0
	for _, f := range c.Fields {
		if l := len(strings.TrimSpace(f.Label)); l > width {
			width = l
		}
	}
	for _, f := range c.Fields {
		label := strings.TrimSpace(f.Label)
		if label == "" {
			continue
		}
		value := strings.TrimSpace(f.Value)
		if value == "" {
			value = "none"
		}
		b.WriteString(fmt.Sprintf("  %s:%s %s\n", label, strings.Repeat(" ", width-len(label)), value))
	}
	return b.String()
}

// RenderError renders the canonical failure shape:
//
//	✖ <what failed>
//	  fix: <command>
func RenderError(ui UI, what, fix string) string {
	var b strings.Builder
	b.WriteString(ui.Red(GlyphFailure) + " " + strings.TrimSpace(what) + "\n")
	if fix != "" {
		b.WriteString("  " + ui.Dim("fix:") + " " + strings.TrimSpace(fix) + "\n")
	}
	return b.String()
}

// DisplayProject renders one project label consistently across cards.
// When the friendly name and ID both exist and differ, it returns
// "<friendly> (<id>)"; otherwise it returns whichever value exists.
func DisplayProject(friendly, id string) string {
	friendly = strings.TrimSpace(friendly)
	id = strings.TrimSpace(id)
	switch {
	case friendly != "" && id != "" && friendly != id:
		return friendly + " (" + id + ")"
	case friendly != "":
		return friendly
	case id != "":
		return id
	default:
		return "<unknown>"
	}
}

// DisplayPath renders a path for humans: relative to root when the path is
// inside it, ~/... when under the home directory, absolute otherwise.
func DisplayPath(root, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if root != "" {
			abs = filepath.Join(root, path)
		} else if wd, err := os.Getwd(); err == nil {
			abs = filepath.Join(wd, path)
		}
	}
	abs = filepath.Clean(abs)
	if root != "" {
		rootAbs := root
		if !filepath.IsAbs(rootAbs) {
			if wd, err := os.Getwd(); err == nil {
				rootAbs = filepath.Join(wd, rootAbs)
			}
		}
		rootAbs = filepath.Clean(rootAbs)
		if rel, err := filepath.Rel(rootAbs, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		if rel, err := filepath.Rel(home, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "~/" + rel
		}
	}
	return abs
}
