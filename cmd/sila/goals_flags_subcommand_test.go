package main

import (
	"flag"
	"strings"
	"testing"
)

func TestGoalsSubcommandFlagRegistration(t *testing.T) {
	fs := flag.NewFlagSet("goals", flag.ContinueOnError)
	rf := newRootFlags(fs, "goals")

	if rf.selectionFlag == nil {
		t.Fatal("expected selectionFlag to be registered")
	}
	if rf.autoRunFlag == nil {
		t.Fatal("expected autoRunFlag to be registered")
	}

	testArgs := []string{
		"--selection=/path/to/selection.json",
		"--auto-run",
		"--commit=a1b2c3d4e5",
	}

	if err := fs.Parse(testArgs); err != nil {
		t.Fatalf("failed to parse registered flags: %v", err)
	}

	if *rf.selectionFlag != "/path/to/selection.json" {
		t.Errorf("selectionFlag = %q, want /path/to/selection.json", *rf.selectionFlag)
	}
	if !*rf.autoRunFlag {
		t.Errorf("autoRunFlag = false, want true")
	}
	if *rf.commitFlag != "a1b2c3d4e5" {
		t.Errorf("commitFlag = %q, want a1b2c3d4e5", *rf.commitFlag)
	}
}

func TestGoalsAcceptFlagsReorderAndParse(t *testing.T) {
	fs := flag.NewFlagSet("goals", flag.ContinueOnError)
	rf := newRootFlags(fs, "goals")

	rawArgs := []string{
		"accept",
		"my-goal",
		"T-001",
		"--selection=fixtures/selection.json",
		"--review=fixtures/review.json",
	}

	flagArgs, pos := reorderFlags(fs, rawArgs)
	if err := fs.Parse(append(flagArgs, pos...)); err != nil {
		t.Fatalf("failed to parse reordered accept flags: %v", err)
	}

	if *rf.selectionFlag != "fixtures/selection.json" {
		t.Errorf("selectionFlag = %q, want fixtures/selection.json", *rf.selectionFlag)
	}
	if *rf.goalReviewFlag != "fixtures/review.json" {
		t.Errorf("goalReviewFlag = %q, want fixtures/review.json", *rf.goalReviewFlag)
	}
}

func TestGoalsCertifyAutoRunReorderAndParse(t *testing.T) {
	fs := flag.NewFlagSet("goals", flag.ContinueOnError)
	rf := newRootFlags(fs, "goals")

	rawArgs := []string{
		"certify",
		"my-goal",
		"--auto-run",
	}

	flagArgs, pos := reorderFlags(fs, rawArgs)
	if err := fs.Parse(append(flagArgs, pos...)); err != nil {
		t.Fatalf("failed to parse reordered certify flags: %v", err)
	}

	if !*rf.autoRunFlag {
		t.Errorf("autoRunFlag = false, want true")
	}
}

func TestGoalsAuditCommitFlagParsing(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		commitMsg  string
		wantCommit string
	}{
		{
			name:       "equals flag",
			args:       []string{"audit", "my-goal", "--commit=fedcba9876"},
			wantCommit: "fedcba9876",
		},
		{
			name:       "space separated flag",
			args:       []string{"audit", "my-goal", "--commit", "1234567890"},
			wantCommit: "1234567890",
		},
		{
			name:       "short alias flag",
			args:       []string{"audit", "my-goal", "-c=9988776655"},
			wantCommit: "9988776655",
		},
		{
			name:       "fallback to root commitMsg",
			args:       []string{"audit", "my-goal"},
			commitMsg:  "abcdef123456",
			wantCommit: "abcdef123456",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gc := &goalsContext{
				commitMsg: tc.commitMsg,
			}
			args := tc.args
			slug := ""
			commitSHA := strings.TrimSpace(gc.commitMsg)

			if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
				slug = args[1]
				args = args[2:]
			} else if len(args) > 0 && args[0] == "audit" {
				args = args[1:]
			}

			for i := 0; i < len(args); i++ {
				a := args[i]
				if after, ok := strings.CutPrefix(a, "--commit="); ok {
					commitSHA = strings.TrimSpace(after)
				} else if after, ok := strings.CutPrefix(a, "-commit="); ok {
					commitSHA = strings.TrimSpace(after)
				} else if after, ok := strings.CutPrefix(a, "-c="); ok {
					commitSHA = strings.TrimSpace(after)
				} else if (a == "--commit" || a == "-commit" || a == "-c") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					commitSHA = strings.TrimSpace(args[i+1])
					i++
				} else if !strings.HasPrefix(a, "-") && slug == "" {
					slug = a
				}
			}

			if slug != "my-goal" {
				t.Fatalf("slug = %q, want my-goal", slug)
			}
			if commitSHA != tc.wantCommit {
				t.Fatalf("commitSHA = %q, want %q", commitSHA, tc.wantCommit)
			}
		})
	}
}
