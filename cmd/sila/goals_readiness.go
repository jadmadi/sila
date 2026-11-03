package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
)

// ReadinessReport represents machine-readable output for goal task contract readiness.
type ReadinessReport struct {
	Ready       bool               `json:"ready"`
	SpecDigest  string             `json:"spec_digest"`
	Diagnostics []goals.Diagnostic `json:"diagnostics"`
}

func (gc *goalsContext) handleReadiness(targetDir string, args []string) {
	jsonOut := gc.jsonOut
	reviewPath := gc.reviewOpt
	var slug string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" || arg == "-json" {
			jsonOut = true
		} else if after, ok := strings.CutPrefix(arg, "--review="); ok {
			reviewPath = after
		} else if after, ok := strings.CutPrefix(arg, "-review="); ok {
			reviewPath = after
		} else if arg == "--review" || arg == "-review" {
			if i+1 < len(args) {
				reviewPath = args[i+1]
				i++
			}
		} else if !strings.HasPrefix(arg, "-") && slug == "" {
			slug = arg
		}
	}

	if slug == "" {
		if jsonOut {
			emitErrorEnvelope("USAGE", "goal slug is required", "sila goals readiness <slug> [--review=<file>] [--json]")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "sila goals readiness: goal slug is required\nUsage: sila goals readiness <slug> [--review=<file>] [--json]\n")
		os.Exit(2)
	}

	slug = filepath.Clean(slug)
	resolvedRoot := goals.ResolveGoalsRootDir(targetDir)
	goalDir := filepath.Join(resolvedRoot, "goals", slug)

	if !goals.FileExists(filepath.Join(goalDir, "goal.md")) {
		if jsonOut {
			emitErrorEnvelope("GOAL_NOT_FOUND", fmt.Sprintf("goal package %q does not exist in goals/%s", slug, slug), "sila goals")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "sila goals readiness: goal package %q does not exist in goals/%s\n", slug, slug)
		os.Exit(2)
	}

	if !goals.FileExists(filepath.Join(goalDir, "work-package.json")) {
		if jsonOut {
			emitErrorEnvelope("WORK_PACKAGE_NOT_FOUND", fmt.Sprintf("goal %q has no work-package.json (legacy goal)", slug), "sila goals readiness <slug> --review=<file>")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "sila goals readiness: goal %q has no work-package.json (legacy goal)\n", slug)
		os.Exit(2)
	}

	pkg, err := goals.LoadWorkPackage(resolvedRoot, slug)
	if err != nil {
		if jsonOut {
			emitErrorEnvelope("WORK_PACKAGE_INVALID", fmt.Sprintf("load work package for %q: %v", slug, err), "sila goals readiness <slug>")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "sila goals readiness: load work package for %q: %v\n", slug, err)
		os.Exit(2)
	}

	specDigest, err := goals.WorkPackageDigest(pkg)
	if err != nil {
		if jsonOut {
			emitErrorEnvelope("WORK_PACKAGE_INVALID", fmt.Sprintf("compute work package digest: %v", err), "sila goals readiness <slug>")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "sila goals readiness: compute work package digest: %v\n", err)
		os.Exit(2)
	}

	// If review file specified, validate and import it
	if reviewPath != "" {
		resolvedReviewPath := reviewPath
		if !filepath.IsAbs(resolvedReviewPath) && !goals.FileExists(resolvedReviewPath) {
			candidate := filepath.Join(resolvedRoot, reviewPath)
			if goals.FileExists(candidate) {
				resolvedReviewPath = candidate
			}
		}

		data, err := os.ReadFile(resolvedReviewPath)
		if err != nil {
			if jsonOut {
				emitErrorEnvelope("REVIEW_FILE_UNREADABLE", fmt.Sprintf("read review file %q: %v", reviewPath, err), "sila goals readiness <slug> --review=<file>")
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "sila goals readiness: read review file %q: %v\n", reviewPath, err)
			os.Exit(2)
		}

		var review goals.SpecReview
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&review); err != nil {
			if jsonOut {
				emitErrorEnvelope("REVIEW_INVALID", fmt.Sprintf("parse review JSON: %v", err), "sila goals readiness <slug> --review=<file>")
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "sila goals readiness: parse review JSON: %v\n", err)
			os.Exit(2)
		}

		reviewDiags := goals.ValidateSpecReview(pkg, review)
		if len(reviewDiags) > 0 {
			// AC-012: Review import with mismatched digest or invalid fields fails without mutating the goal
			if jsonOut {
				rep := ReadinessReport{
					Ready:       false,
					SpecDigest:  specDigest,
					Diagnostics: reviewDiags,
				}
				emitJSON(rep)
			} else {
				fmt.Fprintf(os.Stderr, "spec review validation failed with %d error(s):\n", len(reviewDiags))
				for _, d := range reviewDiags {
					fmt.Fprintf(os.Stderr, "  ✖ [%s] %s: %s\n", d.Code, d.Path, d.Message)
				}
			}
			os.Exit(1)
		}

		if err := goals.SaveSpecReview(resolvedRoot, slug, review); err != nil {
			if jsonOut {
				emitErrorEnvelope("REVIEW_SAVE_FAILED", fmt.Sprintf("save spec review: %v", err), "sila goals readiness <slug> --review=<file>")
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "sila goals readiness: save spec review: %v\n", err)
			os.Exit(2)
		}
	}

	// Validate overall work package readiness
	diags, _ := goals.CheckWorkPackageReady(resolvedRoot, slug)
	isReady := len(diags) == 0
	if diags == nil {
		diags = []goals.Diagnostic{}
	}

	rep := ReadinessReport{
		Ready:       isReady,
		SpecDigest:  specDigest,
		Diagnostics: diags,
	}

	if jsonOut {
		emitJSON(rep)
	} else {
		if isReady {
			fmt.Printf("✔ Goal %q task contracts are READY\n  Spec Digest: %s\n", slug, specDigest)
		} else {
			fmt.Fprintf(os.Stderr, "✖ Goal %q task contracts are NOT READY (%d diagnostic(s))\n  Spec Digest: %s\n", slug, len(diags), specDigest)
			for _, d := range diags {
				fmt.Fprintf(os.Stderr, "  ✖ [%s] %s: %s\n", d.Code, d.Path, d.Message)
			}
		}
	}

	if isReady {
		os.Exit(0)
	}
	os.Exit(1)
}
