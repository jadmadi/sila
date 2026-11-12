package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// CreditEntry represents an acknowledged inspiration or technology.
type CreditEntry struct {
	Entity   string `json:"entity"`
	Author   string `json:"author,omitempty"`
	License  string `json:"license"`
	URL      string `json:"url,omitempty"`
	Category string `json:"category"`
	Impact   string `json:"impact"`
}

var projectCredits = []CreditEntry{
	{
		Entity:   "AXI (Agent eXperience Interface)",
		Author:   "Kun Chen (@kunchenguid)",
		License:  "MIT",
		URL:      "https://github.com/kunchenguid/axi",
		Category: "Agent Ergonomics & CLI Standards",
		Impact:   "Ten principles for agent-ergonomic CLIs: token budget as first-class constraint, content-first execution, minimal schemas, definitive empty states, structured errors.",
	},
	{
		Entity:   "Backpass (Gradient Descent on AGENTS.md)",
		Author:   "Kun Chen (@kunchenguid)",
		License:  "MIT",
		URL:      "https://github.com/kunchenguid/backpass",
		Category: "Instruction Optimization & Token Budgeting",
		Impact:   "Pioneering 'AGENTS.md as weights, transcripts as loss signal, gradient descent on prompt text' with strict token budgeting, verbatim quote floors, and skills as overflow valves.",
	},
	{
		Entity:   "First Mate (Crew Orchestrator & Zero-Token Supervision)",
		Author:   "Kun Chen (@kunchenguid)",
		License:  "MIT",
		URL:      "https://github.com/kunchenguid/firstmate",
		Category: "Multi-Agent Orchestration & Supervision",
		Impact:   "Zero-token multi-agent supervision in tmux sessions with disposable Git worktrees, directly inspiring 'sila crew'.",
	},
	{
		Entity:   "Worktree & Push Gate Proxy (Treehouse & No-Mistakes)",
		Author:   "Kun Chen (@kunchenguid)",
		License:  "MIT",
		URL:      "https://github.com/kunchenguid/treehouse",
		Category: "Worktree Pooling & Pre-Push Verification",
		Impact:   "Go Git worktree pooling and pre-push proxy pipeline in isolated worktrees, inspiring 'sila worktree' and 'sila gate pipeline'.",
	},
	{
		Entity:   "TOON (Token-Oriented Object Notation)",
		Author:   "TOON Specification Authors",
		License:  "MIT",
		URL:      "https://toonformat.dev/",
		Category: "Serialization & Token Efficiency",
		Impact:   "Compact tabular representation delivering ~40% token savings over JSON for LLM consumers while preserving strict structure; implemented natively via '--toon'.",
	},
	{
		Entity:   "Pure-Go SQLite Engine",
		Author:   "Jan Michálek (cznic / modernc.org)",
		License:  "BSD-3-Clause",
		URL:      "https://pkg.go.dev/modernc.org/sqlite",
		Category: "Storage & Systems Foundations",
		Impact:   "100% pure Go CGO-free transpiled SQLite with FTS5, enabling zero-dependency standalone binary distribution and reliable concurrent transactions.",
	},
	{
		Entity:   "go-isatty (Terminal & TTY Detection)",
		Author:   "Yasuhiro Matsumoto (mattn)",
		License:  "MIT",
		URL:      "https://github.com/mattn/go-isatty",
		Category: "Terminal UI & Stream Rendering",
		Impact:   "Lightweight, cross-platform terminal interface detection ensuring ANSI styling is safely stripped when output is redirected to pipes or CI.",
	},
	{
		Entity:   "golang.org/x/sys (OS & System Primitives)",
		Author:   "The Go Authors",
		License:  "BSD-3-Clause",
		URL:      "https://pkg.go.dev/golang.org/x/sys",
		Category: "Systems Primitives & Process Control",
		Impact:   "Low-level system calls, terminal control, raw mode switching, and OS process signalling across Unix and Windows.",
	},
	{
		Entity:   "Sibyl-Memory & Hierarchical Tiers",
		Author:   "Sibyl Labs LLC",
		License:  "MIT",
		URL:      "https://github.com/Sibyl-Labs/Sibyl-Memory",
		Category: "Agent Memory Benchmarks & Hierarchical Architecture",
		Impact:   "Demonstrated on LongMemEval Oracle (#2 globally, 95.6%) that local-first, file-based SQLite with FTS5 and zero vector embeddings outperforms complex vector databases for AI agent long-term memory.",
	},
	{
		Entity:   "MADR (Markdown Architectural Decision Records)",
		Author:   "Oliver Kopp & MADR Community",
		License:  "MIT",
		URL:      "https://adr.github.io/madr/",
		Category: "Architecture Decision Records",
		Impact:   "Structured, version-controlled architectural decision template format powering 'sila adr' authoring, disk-drift checks, and index compilation.",
	},
	{
		Entity:   "Model Context Protocol (MCP)",
		Author:   "Anthropic",
		License:  "MIT",
		URL:      "https://modelcontextprotocol.io/",
		Category: "Agent Protocol & Integration",
		Impact:   "Open protocol standard for LLM tool integration, enabling Sila's 80-tool stdio server for Claude, Cursor, Antigravity, Devin, and any MCP client.",
	},
}

func runCredits(ui render.UI, jsonOut, toonOut bool, args ...string) {
	list := projectCredits
	if len(args) > 0 {
		var filtered []CreditEntry
		for _, arg := range args {
			q := arg
			for _, c := range projectCredits {
				match := containsCI(c.Entity, q) ||
					containsCI(c.Author, q) ||
					containsCI(c.License, q) ||
					containsCI(c.Category, q) ||
					containsCI(c.URL, q) ||
					containsCI(c.Impact, q)
				if match {
					already := false
					for _, existing := range filtered {
						if existing.Entity == c.Entity {
							already = true
							break
						}
					}
					if !already {
						filtered = append(filtered, c)
					}
				}
			}
		}
		list = filtered
	}

	if jsonOut {
		if list == nil {
			list = []CreditEntry{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return
	}

	if toonOut {
		fmt.Printf("credits[%d]{entity,author,license,category,url}:\n", len(list))
		for _, c := range list {
			fmt.Printf("  %q,%q,%q,%q,%q\n", c.Entity, c.Author, c.License, c.Category, c.URL)
		}
		return
	}

	filterNote := ""
	if len(args) > 0 {
		filterNote = fmt.Sprintf(" (filter: %q, %d match)", args[0], len(list))
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · credits & acknowledgments"))+filterNote)
	if len(list) == 0 {
		fmt.Fprintf(os.Stdout, "  No credit entries matching %q found.\n\n", args[0])
		return
	}
	fmt.Fprintln(os.Stdout, "Sila is built on the research, standards, and tools of open-source innovators:")
	fmt.Fprintln(os.Stdout)

	for _, c := range list {
		authorPart := ""
		if c.Author != "" {
			authorPart = " — " + ui.Bold(c.Author)
		}
		fmt.Fprintf(os.Stdout, "  • %s%s\n", ui.Bold(c.Entity), authorPart)
		if c.License != "" {
			fmt.Fprintf(os.Stdout, "    %s: %s\n", ui.Dim("License"), ui.Green(c.License))
		}
		fmt.Fprintf(os.Stdout, "    %s: %s\n", ui.Dim("Category"), c.Category)
		if c.URL != "" {
			fmt.Fprintf(os.Stdout, "    %s: %s\n", ui.Dim("URL"), ui.Cyan(c.URL))
		}
		fmt.Fprintf(os.Stdout, "    %s: %s\n\n", ui.Dim("Impact"), c.Impact)
	}

	fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold("Author:"), "Jad Madi (@jadmadi)")
	fmt.Fprintf(os.Stdout, "  %s %s · %s %s · %s %s\n", ui.Dim("Blog:"), ui.Cyan("https://jadmadi.net"), ui.Dim("X:"), ui.Cyan("https://x.com/jadmadi"), ui.Dim("GitHub:"), ui.Cyan("https://github.com/jadmadi"))
	fmt.Fprintf(os.Stdout, "  %s %s\n\n", ui.Dim("Support my work:"), ui.Cyan("https://paypal.me/madise"))

	fmt.Fprintln(os.Stdout, ui.Dim("Read more in CREDITS.md in the repository root."))
}

func containsCI(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && (s == substr || len(substr) > 0 &&
		(strings.Contains(strings.ToLower(s), strings.ToLower(substr))))
}
