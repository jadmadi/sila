package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runOpinions(ui render.UI, dbPath, project, category, outFile string, jsonOut, toonOut bool, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}

	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}

	switch sub {
	case "add":
		stanceArgs := args[1:]
		if len(stanceArgs) == 0 {
			fmt.Fprintln(os.Stderr, "sila opinions add: specify opinion text (e.g. sila opinions add --category=arch \"Pure-Go SQLite only\")")
			fmt.Fprintln(os.Stdout, "help: sila opinions add [--category=arch|agents|verification|taste] \"<stance>\"")
			os.Exit(2)
		}
		stance := strings.Join(stanceArgs, " ")
		fact, err := dream.AddOpinion(st, project, category, stance, "manual", cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(fact)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Recorded opinion into "+ui.Bold(project)+":")
		fmt.Fprintf(os.Stdout, "  %s\n", fact.Claim)
		fmt.Fprintf(os.Stdout, "\n%s Run 'sila opinions compile --out=OPINIONS.md' to update living documentation.\n", ui.Dim("→"))

	case "list":
		opinions, err := dream.ListOpinions(st, project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if len(opinions) == 0 {
			if jsonOut {
				fmt.Fprintln(os.Stdout, "[]")
			} else if toonOut {
				fmt.Printf("opinions[0]{id,claim,category}:\n")
			} else {
				fmt.Fprintf(os.Stdout, "opinions: 0 opinions found for project %s\n", ui.Bold(project))
				fmt.Fprintf(os.Stdout, "help: use 'sila opinions add \"<stance>\"' to record project technical taste\n")
			}
			return
		}

		if jsonOut {
			emitJSON(opinions)
			return
		}
		if toonOut {
			rows := make([][]string, len(opinions))
			for i, op := range opinions {
				rows[i] = []string{op.ID, op.Claim, op.Kind}
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("opinions", []string{"id", "claim", "kind"}, rows))
			return
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("sila · opinions · %s (%d items)", project, len(opinions)))))
		for _, op := range opinions {
			fmt.Fprintf(os.Stdout, "  • %s\n", op.Claim)
		}
		fmt.Fprintf(os.Stdout, "\n%s Run 'sila opinions compile --out=OPINIONS.md' to generate project document.\n", ui.Dim("→"))

	case "compile", "":
		content, err := dream.CompileOpinions(st, project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}

		if outFile != "" {
			if err := os.WriteFile(outFile, []byte(content), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintln(os.Stdout, ui.Green("✔")+" Compiled living opinions into "+ui.Bold(outFile))
			return
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]string{"project": project, "content": content})
			return
		}

		fmt.Fprint(os.Stdout, content)

	default:
		fmt.Fprintf(os.Stderr, "sila opinions: unknown action %q\n", sub)
		fmt.Fprintln(os.Stdout, "help: valid actions for opinions: compile, add, list (or run without args to print OPINIONS.md)")
		os.Exit(2)
	}
}
