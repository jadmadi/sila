package render

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mattn/go-isatty"
)

// ANSI 24-bit TrueColor codes matching the WCAG-compliant MiMoCode theme (/home/user/.config/opencode/themes/mimocode.json)
const (
	Reset   = "\x1b[0m"
	Bold    = "\x1b[1m"
	Dim     = "\x1b[38;2;128;128;128m" // darkStep11 #808080 (muted text)
	Red     = "\x1b[38;2;251;113;133m" // darkRed #FB7185 (error / blocked / soft rose)
	Green   = "\x1b[38;2;79;214;190m"  // diffAdded #4FD6BE (success / verified / mint teal)
	Yellow  = "\x1b[38;2;251;191;36m"  // darkOrange #FBBF24 (warning / pending / warm amber)
	Cyan    = "\x1b[38;2;255;138;60m"  // darkSecondary #FF8A3C (info / warm secondary orange)
	Magenta = "\x1b[38;2;129;140;248m" // darkAccent #818CF8 (periwinkle lavender accent for projects)
	Orange  = "\x1b[38;2;255;106;0m"   // darkStep9 #FF6A00 (brand primary orange)
	Gold    = "\x1b[38;2;229;192;123m" // darkYellow #E5C07B (syntax type / gold)
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Light-theme replacements for the dark truecolor tokens. Values target WCAG
// AA contrast on a white background.
var light24 = map[string]string{
	Dim:     "\x1b[38;2;107;114;128m", // #6B7280
	Red:     "\x1b[38;2;185;28;28m",   // #B91C1C
	Green:   "\x1b[38;2;4;120;87m",    // #047857
	Yellow:  "\x1b[38;2;180;83;9m",    // #B45309
	Cyan:    "\x1b[38;2;194;65;12m",   // #C2410C
	Magenta: "\x1b[38;2;67;56;202m",   // #4338CA
	Orange:  "\x1b[38;2;194;65;12m",   // #C2410C
	Gold:    "\x1b[38;2;146;64;14m",   // #92400E
}

var dark256 = map[string]string{
	Dim:     "\x1b[38;5;244m",
	Red:     "\x1b[38;5;203m",
	Green:   "\x1b[38;5;79m",
	Yellow:  "\x1b[38;5;214m",
	Cyan:    "\x1b[38;5;209m",
	Magenta: "\x1b[38;5;105m",
	Orange:  "\x1b[38;5;202m",
	Gold:    "\x1b[38;5;180m",
}

var light256 = map[string]string{
	Dim:     "\x1b[38;5;242m",
	Red:     "\x1b[38;5;160m",
	Green:   "\x1b[38;5;29m",
	Yellow:  "\x1b[38;5;130m",
	Cyan:    "\x1b[38;5;166m",
	Magenta: "\x1b[38;5;61m",
	Orange:  "\x1b[38;5;166m",
	Gold:    "\x1b[38;5;94m",
}

var dark16 = map[string]string{
	Dim:     "\x1b[90m",
	Red:     "\x1b[31m",
	Green:   "\x1b[32m",
	Yellow:  "\x1b[33m",
	Cyan:    "\x1b[36m",
	Magenta: "\x1b[35m",
	Orange:  "\x1b[31m",
	Gold:    "\x1b[33m",
}

var light16 = map[string]string{
	Dim:     "\x1b[90m",
	Red:     "\x1b[31m",
	Green:   "\x1b[32m",
	Yellow:  "\x1b[33m",
	Cyan:    "\x1b[36m",
	Magenta: "\x1b[35m",
	Orange:  "\x1b[31m",
	Gold:    "\x1b[33m",
}

// UI controls colorization. Explicit disable always wins; otherwise the
// NO_COLOR/CLICOLOR/TERM/COLORTERM standards decide enablement and depth.
type UI struct {
	noColor bool
	enabled bool
	depth   int // 0 disabled, 16, 256, or 24-bit
	theme   string
	out     *os.File
}

// resolveColorEnabled implements the documented precedence:
// --no-color > NO_COLOR > force-enable > CLICOLOR=0 > TERM=dumb > non-TTY.
func resolveColorEnabled(out *os.File, noColor bool) bool {
	if noColor {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("SILA_FORCE_COLOR") == "1" || os.Getenv("CLICOLOR_FORCE") == "1" {
		return true
	}
	if os.Getenv("CLICOLOR") == "0" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	if out == nil {
		return false
	}
	fi, err := out.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func resolveColorDepth() int {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COLORTERM"))) {
	case "truecolor", "24bit":
		return 24
	}
	if strings.Contains(strings.ToLower(os.Getenv("TERM")), "256color") {
		return 256
	}
	return 16
}

func resolveTheme() string {
	if t := strings.ToLower(strings.TrimSpace(os.Getenv("SILA_THEME"))); t == "light" || t == "dark" {
		return t
	}
	if fgbg := strings.TrimSpace(os.Getenv("COLORFGBG")); fgbg != "" {
		parts := strings.Split(fgbg, ";")
		switch parts[len(parts)-1] {
		case "7", "15", "231":
			return "light"
		}
	}
	return "dark"
}

// New builds a UI from the environment and the explicit --no-color choice.
func New(out *os.File, noColor bool) UI {
	enabled := resolveColorEnabled(out, noColor)
	depth := 0
	if enabled {
		depth = resolveColorDepth()
	}
	return UI{noColor: noColor, enabled: enabled, depth: depth, theme: resolveTheme(), out: out}
}

// ColorDepth reports the negotiated ANSI depth (0 when disabled).
func (u UI) ColorDepth() int { return u.depth }

// Theme reports the selected palette ("dark" or "light").
func (u UI) Theme() string { return u.theme }

// IsTerminal reports whether the output destination is an interactive terminal.
func (u UI) IsTerminal() bool {
	if os.Getenv("SILA_FORCE_TTY") == "1" {
		return true
	}
	if u.out == nil {
		return false
	}
	return isatty.IsTerminal(u.out.Fd()) || isatty.IsCygwinTerminal(u.out.Fd())
}

// ColorEnabled reports whether ANSI terminal color is enabled.
func (u UI) ColorEnabled() bool {
	return u.enabled
}

// colorCode translates a semantic truecolor token to the negotiated theme and
// depth. Bold and Reset pass through unchanged.
func (u UI) colorCode(code string) string {
	switch u.depth {
	case 24:
		if u.theme == "light" {
			if c, ok := light24[code]; ok {
				return c
			}
		}
		return code
	case 256:
		table := dark256
		if u.theme == "light" {
			table = light256
		}
		if c, ok := table[code]; ok {
			return c
		}
	case 16:
		table := dark16
		if u.theme == "light" {
			table = light16
		}
		if c, ok := table[code]; ok {
			return c
		}
	}
	return code
}

// Paint wraps s in the escape code unless color is disabled.
func (u UI) Paint(code, s string) string {
	if !u.enabled {
		return s
	}
	return u.colorCode(code) + s + Reset
}

// Bold formats text with the bold ANSI code if styling is enabled.
func (u UI) Bold(s string) string { return u.Paint(Bold, s) }

// Dim formats text with the dim ANSI code if styling is enabled.
func (u UI) Dim(s string) string { return u.Paint(Dim, s) }

// Red formats text in red ANSI color if styling is enabled.
func (u UI) Red(s string) string { return u.Paint(Red, s) }

// Green formats text in green ANSI color if styling is enabled.
func (u UI) Green(s string) string { return u.Paint(Green, s) }

// Yellow formats text in yellow ANSI color if styling is enabled.
func (u UI) Yellow(s string) string { return u.Paint(Yellow, s) }

// Cyan formats text in cyan ANSI color if styling is enabled.
func (u UI) Cyan(s string) string { return u.Paint(Cyan, s) }

// Magenta formats text in magenta ANSI color if styling is enabled.
func (u UI) Magenta(s string) string { return u.Paint(Magenta, s) }

// Orange formats text in orange ANSI color if styling is enabled.
func (u UI) Orange(s string) string { return u.Paint(Orange, s) }

// Accent formats text with the UI accent color (magenta) if styling is enabled.
func (u UI) Accent(s string) string { return u.Paint(Magenta, s) }

// Primary formats text with the UI primary color (orange) if styling is enabled.
func (u UI) Primary(s string) string { return u.Paint(Orange, s) }

// Gold formats text with the gold ANSI color if styling is enabled.
func (u UI) Gold(s string) string { return u.Paint(Gold, s) }

// visibleWidth returns the display width of s ignoring ANSI escape codes.
func visibleWidth(s string) int {
	return len(ansiRE.ReplaceAllString(s, ""))
}

// VisibleLen returns the display width of s with ANSI escape codes stripped.
// Callers that assemble their own terminal output use it to keep every line
// inside a fixed column budget.
func VisibleLen(s string) int {
	return visibleWidth(s)
}

// Wrap breaks text into lines of at most budget columns and prefixes every line
// after the first with indent, so a long excerpt still reads as one entry
// rather than looking like several. Callers pass an indent as wide as the
// prefix already printed on the first line, which keeps every line ending at
// the same column.
func Wrap(text string, budget int, indent string) string {
	if budget <= 0 {
		return text
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var b strings.Builder
	line := 0
	for i, w := range words {
		switch {
		case i == 0:
			b.WriteString(w)
			line = len(w)
		case line+1+len(w) > budget:
			b.WriteString("\n" + indent + w)
			line = len(w)
		default:
			b.WriteString(" " + w)
			line += 1 + len(w)
		}
	}
	return b.String()
}

// Table renders an aligned box-drawing table. aligns selects left/right
// alignment per column ('l' elsewhere = 'r'). Header and cell strings may
// already contain ANSI codes; widths are computed on visible text.
func (u UI) Table(headers []string, rows [][]string, aligns []string) string {
	cols := len(headers)
	w := make([]int, cols)
	for c := range cols {
		w[c] = visibleWidth(headers[c])
	}
	for _, r := range rows {
		for c := 0; c < cols && c < len(r); c++ {
			if v := visibleWidth(r[c]); v > w[c] {
				w[c] = v
			}
		}
	}
	// border segments are driven by the computed column widths, so the frame
	// matches the padded content lines exactly.
	segment := func(c int) string { return strings.Repeat("─", w[c]+2) }
	frame := func(sep string) string {
		parts := make([]string, 0, cols)
		for c := range cols {
			parts = append(parts, segment(c))
		}
		return strings.Join(parts, sep)
	}

	b := strings.Builder{}
	line := func(left, mid, right string, cells []string) {
		b.WriteString(left)
		for c := range cols {
			if c > 0 {
				b.WriteString(mid)
			}
			cell := cells[c]
			pad := w[c] - visibleWidth(cell)
			if c < len(aligns) && aligns[c] == "l" {
				cell += strings.Repeat(" ", pad)
			} else {
				cell = strings.Repeat(" ", pad) + cell
			}
			b.WriteString(" " + cell + " ")
		}
		b.WriteString(right)
		b.WriteString("\n")
	}

	b.WriteString("┌" + frame("┬") + "┐\n")
	line("│", "│", "│", headers)
	b.WriteString("├" + frame("┼") + "┤\n")
	for _, r := range rows {
		line("│", "│", "│", r)
	}
	b.WriteString("└" + frame("┴") + "┘")
	return b.String()
}

// FormatBytes renders a byte count in a compact human form.
func FormatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// FormatNumber renders an integer formatted with comma separators (e.g. 17,793).
func FormatNumber(n int64) string {
	in := fmt.Sprintf("%d", n)
	if len(in) <= 3 {
		return in
	}
	var out []byte
	rem := len(in) % 3
	if rem > 0 {
		out = append(out, in[:rem]...)
		if len(in) > rem {
			out = append(out, ',')
		}
	}
	for i := rem; i < len(in); i += 3 {
		out = append(out, in[i:i+3]...)
		if i+3 < len(in) {
			out = append(out, ',')
		}
	}
	return string(out)
}

// RenderSilaStickyInstructions returns the prompt instructions describing
// the /sila slash command mode across agent instruction targets (CLAUDE.md, GEMINI.md, Cursor rules).
func RenderSilaStickyInstructions() string {
	return `## Sila Sticky Mode (/sila)

Typing /sila <goal> invokes the autonomous stage-gate execution engine:
1. Orient and Prime: Executes sila salam and sila prime to load active repo context.
2. Goal Discovery: Evaluates sila goals ready and identifies candidate tasks.
3. Lock and Charter: Acquires goal claim lock with boundary blast-radius enforcement.
4. Playbook Execution: Matches and executes specialized playbooks (runtime-forensics, visual-parity, etc.).
5. Pre-Commit Gate: Runs sila gate check and verifies zero regressions with go test -race ./...
6. Mystery Audit: Executes independent black-box audit probes before certification.
7. Communication Hygiene: Runs sila unslop to ensure plain English communications.
`
}
