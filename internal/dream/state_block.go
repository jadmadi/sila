package dream

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// StateBlockBudget is the default byte ceiling for the pushed state block.
const StateBlockBudget = 1200

// WelcomeBackDelta counts what changed while the user was away.
type WelcomeBackDelta struct {
	Gap       time.Duration
	Decisions int
	Lessons   int
	Handoffs  int
	Capped    bool
}

// StateBlockInput is the pure input for BuildStateBlock. Delta is nil when the
// gap is shorter than the configured window (or disabled).
type StateBlockInput struct {
	ProjectID      string
	Now            time.Time
	LastSessionEnd time.Time
	Delta          *WelcomeBackDelta
	Cards          []store.SessionCard
	Facts          []store.Fact // memos and lessons
	Goals          []goals.Goal
	ReadyGoals     int
}

// BuildWelcomeBackDelta counts decisions, lessons, and handoffs created after
// since. It returns ok=false when the gap is shorter than window, window is
// zero, or since is in the future.
func BuildWelcomeBackDelta(st *store.Store, projectID string, since, now time.Time, window time.Duration) (WelcomeBackDelta, bool) {
	if st == nil || window <= 0 || since.IsZero() || !now.After(since) {
		return WelcomeBackDelta{}, false
	}
	gap := now.Sub(since)
	if gap < window {
		return WelcomeBackDelta{}, false
	}
	sinceStr := since.UTC().Format(time.RFC3339)
	facts, err := st.FactsSince(projectID, sinceStr, 200)
	if err != nil {
		return WelcomeBackDelta{}, false
	}
	handoffs, err := st.HandoffsSince(projectID, sinceStr, 200)
	if err != nil {
		return WelcomeBackDelta{}, false
	}
	delta := WelcomeBackDelta{Gap: gap}
	for _, f := range facts {
		switch strings.ToLower(f.Kind) {
		case "memo":
			delta.Decisions++
		case "lesson":
			delta.Lessons++
		}
	}
	delta.Handoffs = len(handoffs)
	if len(facts) >= 200 || len(handoffs) >= 200 {
		delta.Capped = true
	}
	return delta, true
}

// BuildStateBlock renders the compact continuity block. It performs no I/O and
// reads no clock: every variable comes from the input.
func BuildStateBlock(in StateBlockInput, budget int) string {
	if budget <= 0 {
		budget = StateBlockBudget
	}
	sections := stateSections(in)
	if len(sections) == 0 {
		return ""
	}
	for _, key := range []string{"goals", "pitfalls", "decisions", "delta", "next", "last"} {
		if blockBytes(sections) <= budget {
			break
		}
		delete(sections, key)
	}
	out := renderSections(sections)
	if len(out) <= budget {
		return out
	}
	// Last resort: cut on a rune boundary after the header.
	out = clipRunes(strings.TrimSuffix(out, "\n"), maxRunesForBytes(out, budget))
	return out + "\n"
}

func stateSections(in StateBlockInput) map[string]string {
	sections := map[string]string{}
	if card := newestCard(in.Cards); card != nil {
		headline := firstText(card.Outcome, card.Intent, "(partial card)")
		line := fmt.Sprintf("- Last session (%s, %s): %s", card.Agent, relativeAge(in.Now, card.CreatedAt), clipRunes(headline, 100))
		if in.Now.Sub(card.CreatedAt) > 14*24*time.Hour {
			line += " (stale)"
		}
		sections["last"] = line + "\n"
	} else if !in.LastSessionEnd.IsZero() {
		sections["last"] = fmt.Sprintf("- Last session ended %s\n", relativeAge(in.Now, in.LastSessionEnd))
	}
	if in.Delta != nil {
		parts := make([]string, 0, 3)
		if in.Delta.Decisions > 0 {
			parts = append(parts, countLabel(in.Delta.Decisions, "decision"))
		}
		if in.Delta.Lessons > 0 {
			parts = append(parts, countLabel(in.Delta.Lessons, "lesson"))
		}
		if in.Delta.Handoffs > 0 {
			parts = append(parts, countLabel(in.Delta.Handoffs, "handoff"))
		}
		if len(parts) > 0 {
			suffix := ""
			if in.Delta.Capped {
				suffix = "+"
			}
			line := fmt.Sprintf("- Since you left (%s): %s%s\n", humanAge(in.Now, in.Now.Add(-in.Delta.Gap)), strings.Join(parts, ", "), suffix)
			sections["delta"] = line
		}
	}
	next := nextAction(in.Cards, in.Facts)
	if next != "" {
		sections["next"] = "- Next: " + clipRunes(next, 120) + "\n"
	}
	if decisions := decisionLine(in.Facts); decisions != "" {
		sections["decisions"] = "- Decisions: " + decisions + "\n"
	}
	if pitfalls := pitfallLine(in.Facts); pitfalls != "" {
		sections["pitfalls"] = "- Pitfalls: " + pitfalls + "\n"
	}
	if goalLine := goalsLine(in.Goals, in.ReadyGoals); goalLine != "" {
		sections["goals"] = "- Goals: " + goalLine + "\n"
	}
	return sections
}

func renderSections(sections map[string]string) string {
	if len(sections) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Continuity state\n")
	for _, key := range []string{"last", "delta", "next", "decisions", "pitfalls", "goals"} {
		b.WriteString(sections[key])
	}
	return b.String()
}

func blockBytes(sections map[string]string) int {
	n := len("### Continuity state\n")
	for _, v := range sections {
		n += len(v)
	}
	return n
}

func newestCard(cards []store.SessionCard) *store.SessionCard {
	if len(cards) == 0 {
		return nil
	}
	newest := cards[0]
	for _, c := range cards[1:] {
		if c.CreatedAt.After(newest.CreatedAt) {
			newest = c
		}
	}
	return &newest
}

func nextAction(cards []store.SessionCard, facts []store.Fact) string {
	if card := newestCard(cards); card != nil && card.NextAction.Text != "" && card.NextAction.Text != "none" {
		return redactCardText(card.NextAction.Text, "")
	}
	sorted := sortedFacts(facts)
	for _, f := range sorted {
		if strings.EqualFold(f.Kind, "memo") && strings.TrimSpace(f.Claim) != "" {
			return redactCardText(firstLine(f.Claim), "")
		}
	}
	return ""
}

func decisionLine(facts []store.Fact) string {
	items := make([]string, 0, 2)
	for _, f := range sortedFacts(facts) {
		if !strings.EqualFold(f.Kind, "memo") {
			continue
		}
		text := clipRunes(redactCardText(firstLine(f.Claim), ""), 90)
		if text == "" {
			continue
		}
		items = append(items, text)
		if len(items) == 2 {
			break
		}
	}
	return strings.Join(items, "; ")
}

func pitfallLine(facts []store.Fact) string {
	for _, f := range sortedFacts(facts) {
		if !strings.EqualFold(f.Kind, "lesson") || !f.AutoPromoted {
			continue
		}
		if text := clipRunes(redactCardText(firstLine(f.Claim), ""), 110); text != "" {
			return text
		}
	}
	for _, f := range sortedFacts(facts) {
		if !strings.EqualFold(f.Kind, "lesson") {
			continue
		}
		text := clipRunes(redactCardText(firstLine(f.Claim), ""), 110)
		if text != "" {
			return text
		}
	}
	return ""
}

func goalsLine(list []goals.Goal, ready int) string {
	inflight := 0
	var slugs []string
	for _, g := range list {
		status := strings.ToLower(g.Status)
		if status == "implemented" || status == "superseded" {
			continue
		}
		inflight++
		if g.Slug != "" {
			slugs = append(slugs, g.Slug)
		}
	}
	if inflight == 0 && ready == 0 {
		return ""
	}
	sort.Strings(slugs)
	line := fmt.Sprintf("%d in flight", inflight)
	if len(slugs) > 0 {
		line += " (" + slugs[0] + ")"
	}
	line += fmt.Sprintf("; %d ready", ready)
	return line
}

func sortedFacts(facts []store.Fact) []store.Fact {
	out := append([]store.Fact(nil), facts...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FirstSeen != out[j].FirstSeen {
			return out[i].FirstSeen > out[j].FirstSeen
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func relativeAge(now, then time.Time) string {
	age := humanAge(now, then)
	if age == "just now" || age == "unknown" {
		return age
	}
	return age + " ago"
}

func humanAge(now, then time.Time) string {
	if then.IsZero() {
		return "unknown"
	}
	if !now.After(then) {
		return "just now"
	}
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func countLabel(n int, singular string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %ss", n, singular)
}

func firstLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func firstText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxRunesForBytes(s string, maxBytes int) int {
	if maxBytes <= 0 {
		return 0
	}
	n, used := 0, 0
	for _, r := range s {
		size := utf8.RuneLen(r)
		if used+size > maxBytes-3 {
			break
		}
		used += size
		n++
	}
	return n
}

// LoadStateBlock assembles and renders the state block for a project. Any store
// read failure degrades to an empty block; prime never fails because of it.
func LoadStateBlock(ctx context.Context, st *store.Store, projectID, projectDir string, now time.Time, welcomeWindow time.Duration, budget int) string {
	if st == nil || projectID == "" {
		return ""
	}
	cards, err := st.LatestSessionCards(ctx, projectID, 3)
	if err != nil {
		cards = nil
	}
	// A continuity block needs a last session to anchor it. Goals and facts
	// alone must not change prime output on a fresh project.
	if len(cards) == 0 {
		return ""
	}
	memos, _ := st.ListFactsByKind("memo", projectID, time.Time{}, 20)
	lessons, _ := st.ListFactsByKind("lesson", projectID, time.Time{}, 20)
	facts := append(append([]store.Fact(nil), memos...), lessons...)
	if promoted, perr := st.PromotedLessons(projectID, 5); perr == nil {
		seen := make(map[string]bool, len(facts))
		for _, f := range facts {
			seen[f.ID] = true
		}
		for _, f := range promoted {
			if !seen[f.ID] {
				facts = append(facts, f)
			}
		}
	}

	var goalList []goals.Goal
	ready := 0
	if projectDir != "" {
		if audit, err := goals.ScanGoals(projectDir); err == nil && audit != nil {
			goalList = audit.Goals
			for _, g := range goalList {
				if isReadyGoal(g) {
					ready++
				}
			}
		}
	}

	in := StateBlockInput{
		ProjectID:  projectID,
		Now:        now,
		Cards:      cards,
		Facts:      facts,
		Goals:      goalList,
		ReadyGoals: ready,
	}
	if len(cards) > 0 {
		in.LastSessionEnd = cards[0].CreatedAt
	}
	if welcomeWindow > 0 && !in.LastSessionEnd.IsZero() {
		if delta, ok := BuildWelcomeBackDelta(st, projectID, in.LastSessionEnd, now, welcomeWindow); ok {
			in.Delta = &delta
		}
	}
	return BuildStateBlock(in, budget)
}

func isReadyGoal(g goals.Goal) bool {
	if strings.ToLower(g.Status) != "pending" {
		return false
	}
	return g.Independent && !g.Blocked
}
