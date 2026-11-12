package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// cardModeEnabled reports whether the stop hook should write a session card.
func cardModeEnabled() bool {
	mode := os.Getenv("SILA_CARD_MODE")
	return mode != "off"
}

// saveSessionCardFromHandoff writes one typed card at session end. It prefers
// the handoff record for the summary, open items, and touched files, and falls
// back to a partial card when the handoff is missing.
func saveSessionCardFromHandoff(st *store.Store, projectID, cwd, sessionID, agent, handoffID string) error {
	if !cardModeEnabled() {
		return nil
	}
	if st == nil {
		return fmt.Errorf("store is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var messages []dream.CardMessage
	var openItems []string
	var files []string
	if handoffID != "" {
		if h, err := st.GetHandoff(ctx, handoffID); err == nil && h != nil {
			openItems = h.OpenItems
			files = h.RepoFiles
			if h.Summary != "" {
				messages = []dream.CardMessage{{Role: "assistant", Content: h.Summary}}
			}
		}
	}
	card, err := dream.BuildSessionCard(dream.CardInput{
		Now:          time.Now().UTC(),
		ProjectID:    projectID,
		SessionID:    sessionID,
		Agent:        agent,
		ProjectDir:   cwd,
		HomeDir:      os.Getenv("HOME"),
		Transcript:   handoffID,
		Messages:     messages,
		FilesTouched: files,
		OpenItems:    openItems,
	})
	if err != nil {
		return err
	}
	_, err = st.SaveSessionCard(ctx, card)
	return err
}

// runCards implements `sila cards list|show`.
func runCards(_ render.UI, dbPath, project string, jsonOut bool, args []string) {
	args, jsonOut = splitConnectJSON(args, jsonOut)
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	cwd, _ := os.Getwd()
	projectID := project
	if projectID == "" {
		projectID = store.ProjectID(cwd)
	}
	st, _, err := openReadStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila cards: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch sub {
	case "list":
		limit := 10
		for i := 0; i < len(args); i++ {
			if strings.HasPrefix(args[i], "--limit=") {
				fmt.Sscanf(strings.TrimPrefix(args[i], "--limit="), "%d", &limit)
			}
		}
		if limit <= 0 || limit > 50 {
			fmt.Fprintln(os.Stderr, "usage: sila cards list [--limit=1..50]")
			os.Exit(2)
		}
		cards, err := st.LatestSessionCards(ctx, projectID, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila cards: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(cards)
			return
		}
		if len(cards) == 0 {
			fmt.Println("no session cards for this project")
			return
		}
		fmt.Printf("%-24s %-14s %-8s %-17s %s\n", "CARD", "SESSION", "AGENT", "CREATED", "OUTCOME")
		for _, c := range cards {
			fmt.Printf("%-24s %-14s %-8s %-17s %s\n",
				shortCardID(c.ID), clipCardText(c.SessionID, 14), clipCardText(c.Agent, 8),
				c.CreatedAt.UTC().Format("2006-01-02 15:04"), clipCardText(firstNonEmpty(c.Outcome, c.Intent), 60))
		}
	case "show":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: sila cards show <card-id|session-id>")
			os.Exit(2)
		}
		key := args[0]
		card, ok, err := loadCard(ctx, st, projectID, key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila cards: %v\n", err)
			os.Exit(1)
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "card not found")
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(card)
			return
		}
		printCard(card)
	default:
		fmt.Fprintf(os.Stderr, "unknown cards command %q\n", sub)
		os.Exit(2)
	}
}

func loadCard(ctx context.Context, st *store.Store, projectID, key string) (store.SessionCard, bool, error) {
	if strings.HasPrefix(key, "sha256:") {
		return st.GetSessionCard(ctx, key)
	}
	id := store.SessionCardID(projectID, key)
	card, ok, err := st.GetSessionCard(ctx, id)
	if err != nil || ok {
		return card, ok, err
	}
	return store.SessionCard{}, false, nil
}

func printCard(c store.SessionCard) {
	fmt.Printf("%s\n", c.ID)
	fmt.Printf("  session: %s\n  agent:   %s\n  created: %s\n", c.SessionID, c.Agent, c.CreatedAt.UTC().Format(time.RFC3339))
	if c.Intent != "" {
		fmt.Printf("  intent:  %s\n", c.Intent)
	}
	if c.Outcome != "" {
		fmt.Printf("  outcome: %s\n", c.Outcome)
	}
	if c.NextAction.Text != "" {
		fmt.Printf("  next:    %s (%s)\n", c.NextAction.Text, c.NextAction.Owner)
	}
	for _, d := range c.Decisions {
		fmt.Printf("  decision: %s\n", d.Text)
	}
	for _, q := range c.OpenQuestions {
		fmt.Printf("  open:    %s\n", q)
	}
	if len(c.FilesTouched) > 0 {
		fmt.Printf("  files:   %s\n", strings.Join(c.FilesTouched, ", "))
	}
	if c.Partial {
		fmt.Println("  (partial)")
	}
}

func shortCardID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 16 {
		return id[:16]
	}
	return id
}

func clipCardText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// stateBlockWindow resolves the welcome-back window from config, defaulting to
// six hours when the config cannot be read.
func stateBlockWindow() time.Duration {
	cfg, err := config.Load("")
	if err != nil {
		return 6 * time.Hour
	}
	return cfg.WelcomeBackWindow()
}
