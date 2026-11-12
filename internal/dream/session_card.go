package dream

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/store"
)

// CardMessage is one transcript turn used to build a session card.
type CardMessage struct {
	Role    string
	Content string
	TS      time.Time
}

// CardInput is the pure input for BuildSessionCard.
type CardInput struct {
	Now          time.Time
	ProjectID    string
	SessionID    string
	Agent        string
	ProjectDir   string
	HomeDir      string
	Transcript   string
	Messages     []CardMessage
	FilesTouched []string
	OpenItems    []string
}

const (
	cardTitleRunes  = 160
	cardDecisionCap = 8
	cardFileCap     = 20
	cardQuestionCap = 8
	cardPitfallCap  = 8
)

var (
	cardSecretRE = regexp.MustCompile(`(?i)\b(token|secret|password|api[_-]?key)\s*[:=]\s*\S+`)
	cardFenceRE  = regexp.MustCompile("(?s)```.*?```")
	cardAbsRE    = regexp.MustCompile(`(^|[\s(\[{"'=:,;])(/[A-Za-z0-9._\-+/]+)`)
)

// BuildSessionCard produces a deterministic, size-bounded card. It performs no
// I/O and reads no clock: every variable comes from the input.
func BuildSessionCard(in CardInput) (store.SessionCard, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	card := store.SessionCard{
		Schema:    store.SessionCardSchema,
		ID:        store.SessionCardID(in.ProjectID, in.SessionID),
		ProjectID: in.ProjectID,
		SessionID: in.SessionID,
		Agent:     in.Agent,
		CreatedAt: now.UTC(),
		Source: store.CardSource{
			Transcript: in.Transcript,
			Messages:   len(in.Messages),
			Generator:  "deterministic",
		},
	}
	if len(in.Messages) == 0 {
		card.Partial = true
	}
	card.Intent = cardIntent(in.Messages)
	card.Outcome = cardOutcome(in.Messages)
	card.Decisions = cardDecisions(in.Messages)
	card.FilesTouched = cardFiles(in.FilesTouched, in.ProjectDir, in.HomeDir)
	card.OpenQuestions = cardQuestions(in.OpenItems)
	card.NextAction = cardNextAction(in.OpenItems)
	card.Pitfalls = nil
	card = truncateCard(card)
	card.Intent = redactCardText(card.Intent, in.HomeDir)
	card.Outcome = redactCardText(card.Outcome, in.HomeDir)
	return card, nil
}

func cardIntent(messages []CardMessage) string {
	for _, m := range messages {
		if m.Role == "user" {
			return clipRunes(cleanCardText(m.Content), cardTitleRunes)
		}
	}
	return ""
}

func cardOutcome(messages []CardMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return clipRunes(cleanCardText(messages[i].Content), cardTitleRunes)
		}
	}
	return ""
}

func cardDecisions(messages []CardMessage) []store.CardDecision {
	seen := map[string]bool{}
	out := make([]store.CardDecision, 0, cardDecisionCap)
	inDecisions := false
	for _, m := range messages {
		for _, raw := range strings.Split(m.Content, "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasPrefix(line, "## ") {
				inDecisions = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "##")), "decisions")
				continue
			}
			var text string
			lower := strings.ToLower(line)
			switch {
			case strings.HasPrefix(lower, "decision:"):
				text = strings.TrimSpace(line[len("decision:"):])
			case strings.HasPrefix(lower, "decided:"):
				text = strings.TrimSpace(line[len("decided:"):])
			case inDecisions && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")):
				text = strings.TrimSpace(line[2:])
			default:
				continue
			}
			text = clipRunes(cleanCardText(text), 200)
			if text == "" || seen[text] {
				continue
			}
			seen[text] = true
			out = append(out, store.CardDecision{Text: text, Confidence: "medium"})
			if len(out) == cardDecisionCap {
				return out
			}
		}
	}
	return out
}

func cardFiles(files []string, projectDir, homeDir string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, cardFileCap)
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		f = strings.ReplaceAll(f, "\\", "/")
		if homeDir != "" {
			f = strings.ReplaceAll(f, homeDir, "~")
		}
		if projectDir != "" && filepath.IsAbs(f) {
			rel, err := filepath.Rel(projectDir, f)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			f = filepath.ToSlash(rel)
		}
		if strings.HasPrefix(f, "~") || strings.HasPrefix(f, "/") {
			continue
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	if len(out) > cardFileCap {
		out = out[:cardFileCap]
	}
	return out
}

func cardQuestions(items []string) []string {
	out := make([]string, 0, cardQuestionCap)
	for _, item := range items {
		item = clipRunes(cleanCardText(item), 160)
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) == cardQuestionCap {
			break
		}
	}
	return out
}

func cardNextAction(items []string) store.CardNextAction {
	for _, item := range items {
		item = clipRunes(cleanCardText(item), 160)
		if item != "" {
			return store.CardNextAction{Text: item, Owner: "agent"}
		}
	}
	return store.CardNextAction{Text: "none", Owner: "agent"}
}

func cleanCardText(s string) string {
	s = cardFenceRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func redactCardText(s, homeDir string) string {
	s = cardSecretRE.ReplaceAllString(s, "$1=[redacted]")
	if homeDir != "" {
		s = strings.ReplaceAll(s, homeDir, "~")
	}
	return s
}

func clipRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}

func truncateCard(card store.SessionCard) store.SessionCard {
	for {
		payload, err := json.Marshal(card)
		if err != nil || len(payload) <= store.SessionCardMaxBytes {
			return card
		}
		card.Source.Truncated = true
		switch {
		case len(card.Pitfalls) > 0:
			card.Pitfalls = card.Pitfalls[:len(card.Pitfalls)-1]
		case len(card.Decisions) > 0:
			card.Decisions = card.Decisions[:len(card.Decisions)-1]
		case len(card.OpenQuestions) > 0:
			card.OpenQuestions = card.OpenQuestions[:len(card.OpenQuestions)-1]
		case len(card.FilesTouched) > 0:
			card.FilesTouched = card.FilesTouched[:len(card.FilesTouched)-1]
		case utf8.RuneCountInString(card.NextAction.Text) > 16:
			card.NextAction.Text = clipRunes(card.NextAction.Text, 16)
		case utf8.RuneCountInString(card.Outcome) > 16:
			card.Outcome = clipRunes(card.Outcome, 16)
		case utf8.RuneCountInString(card.Intent) > 16:
			card.Intent = clipRunes(card.Intent, 16)
		case card.Source.Transcript != "":
			card.Source.Transcript = ""
		default:
			return card
		}
	}
}

// FormatSessionCards renders at most three cards as a bounded markdown section.
// It returns the empty string when there are no cards, so callers can append it
// unconditionally.
func FormatSessionCards(cards []store.SessionCard, maxCards int) string {
	if len(cards) == 0 {
		return ""
	}
	if maxCards <= 0 || maxCards > 3 {
		maxCards = 3
	}
	const budget = 1600
	var b strings.Builder
	b.WriteString("\n### Recent session cards\n")
	for i, c := range cards {
		if i >= maxCards {
			break
		}
		headline := c.Outcome
		if headline == "" {
			headline = c.Intent
		}
		if headline == "" {
			headline = "(partial card)"
		}
		when := c.CreatedAt.UTC().Format("2006-01-02 15:04")
		line := "- " + when + " · " + c.Agent + " · " + clipRunes(headline, 120) + "\n"
		if c.NextAction.Text != "" && c.NextAction.Text != "none" {
			line += "  Next: " + clipRunes(c.NextAction.Text, 120) + "\n"
		}
		if b.Len()+len(line) > budget {
			break
		}
		b.WriteString(line)
	}
	return b.String()
}
