package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// MaxRuleFileBytes is the Antigravity context ceiling (24,000 bytes) for instruction files.
const MaxRuleFileBytes = 24000

// SyncResult details the outcome (or the preview plan) of target projection
// deployment. Preview and execution derive from the same plan: with
// CompileOptions.Preview set, nothing is written and every planned write is
// returned in Plan with its diff.
type SyncResult struct {
	RepoRoot        string         `json:"repo_root"`
	FilesWritten    []string       `json:"files_written"`
	Symlinks        []string       `json:"symlinks"`
	Errors          []string       `json:"errors,omitempty"`
	Preview         bool           `json:"preview,omitempty"`
	Atomic          bool           `json:"atomic"`
	RolledBack      bool           `json:"rolled_back,omitempty"`
	FailureStage    string         `json:"failure_stage,omitempty"`
	FilesSkipped    int            `json:"files_skipped,omitempty"`
	ManagedHash     string         `json:"managed_hash,omitempty"`
	ManagedRevision int            `json:"managed_revision,omitempty"`
	HashSkips       int            `json:"hash_skips,omitempty"`
	ChangedOnly     bool           `json:"changed_only"`
	Forced          bool           `json:"forced"`
	Written         int            `json:"written"`
	Skipped         int            `json:"skipped"`
	Failed          int            `json:"failed"`
	Plan            []PlannedWrite `json:"plan,omitempty"`
}

// SyncPlan is the read-only per-project plan. Writes are committed only when
// Atomic is true and Errors is empty.
type SyncPlan struct {
	Writes []PlannedWrite `json:"writes"`
	Errors []string       `json:"errors,omitempty"`
	Atomic bool           `json:"atomic"`
	Result *SyncResult    `json:"-"`
}

// PlannedWrite is one concrete file change (or skip) the deployer would make.
type PlannedWrite struct {
	Path                string `json:"path"`
	Action              string `json:"action"` // create | merge | replace | write | symlink | skip
	Detail              string `json:"detail,omitempty"`
	BeforeBytes         int    `json:"before_bytes,omitempty"`
	AfterBytes          int    `json:"after_bytes,omitempty"`
	ManagedHash         string `json:"managed_hash,omitempty"`
	ExistingManagedHash string `json:"existing_managed_hash,omitempty"`
	ManagedRevision     int    `json:"managed_revision,omitempty"`
	HashMatch           bool   `json:"hash_match,omitempty"`
	SkipReason          string `json:"skip_reason,omitempty"`
	Diff                string `json:"diff,omitempty"`
}

// readExisting returns the current content of path, or "" when absent.
func readExisting(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// DeployTargets writes or updates target rule projections across tools.
// It plans with writes disabled, then commits only when the plan has zero
// errors. Existing user content is preserved via owned-block merge; whole-file
// replacement of a user-owned file requires opts.Replace.
func DeployTargets(repoRoot string, opts CompileOptions, tools []string) (*SyncResult, error) {
	plan, err := planTargets(repoRoot, opts, tools)
	if err != nil {
		return nil, err
	}
	return commitSyncPlan(repoRoot, opts, tools, plan)
}

// planTargets builds the complete per-project plan with writes disabled.
func planTargets(repoRoot string, opts CompileOptions, tools []string) (*SyncPlan, error) {
	planOpts := opts
	planOpts.Preview = true
	res, err := deployTargetsOnce(repoRoot, planOpts, tools)
	if err != nil {
		return nil, err
	}
	plan := &SyncPlan{Writes: res.Plan, Errors: res.Errors, Result: res}
	plan.Atomic = len(plan.Errors) == 0
	for _, write := range plan.Writes {
		if write.Action == "error" {
			plan.Atomic = false
		}
	}
	res.Atomic = plan.Atomic
	res.FilesSkipped = countSkipped(plan.Writes)
	finalizeSyncResult(res, opts)
	return plan, nil
}

// commitSyncPlan writes a clean plan and rolls back every touched path if a
// later write fails. A preflight failure writes nothing.
func commitSyncPlan(repoRoot string, opts CompileOptions, tools []string, plan *SyncPlan) (*SyncResult, error) {
	if plan == nil || plan.Result == nil {
		return nil, fmt.Errorf("sync plan is nil")
	}
	res := plan.Result
	res.Atomic = plan.Atomic
	res.FilesSkipped = countSkipped(plan.Writes)
	finalizeSyncResult(res, opts)
	if !plan.Atomic || opts.Preview {
		res.FailureStage = ""
		if !plan.Atomic {
			res.FailureStage = "plan"
		}
		return res, nil
	}

	snapshots, err := snapshotPlanPaths(repoRoot, plan.Writes)
	if err != nil {
		res.Errors = append(res.Errors, "snapshot: "+err.Error())
		res.Atomic = false
		res.FailureStage = "plan"
		return res, nil
	}
	exec, err := deployTargetsOnce(repoRoot, opts, tools)
	if err != nil {
		exec = &SyncResult{RepoRoot: repoRoot, Errors: []string{err.Error()}, Plan: plan.Writes}
	}
	exec.Atomic = len(exec.Errors) == 0
	exec.FilesSkipped = countSkipped(plan.Writes)
	if len(exec.Errors) > 0 {
		exec.FailureStage = "commit"
		exec.Atomic = false
		exec.FilesWritten = nil
		exec.Symlinks = nil
		if rollbackErr := restoreSnapshots(snapshots); rollbackErr != nil {
			exec.FailureStage = "rollback"
			exec.Errors = append(exec.Errors, "rollback: "+rollbackErr.Error())
		} else {
			exec.RolledBack = true
		}
	}
	finalizeSyncResult(exec, opts)
	return exec, nil
}

func finalizeSyncResult(res *SyncResult, opts CompileOptions) {
	if res == nil {
		return
	}
	res.ChangedOnly = !opts.Force
	res.Forced = opts.Force
	res.Written = len(res.FilesWritten) + len(res.Symlinks)
	res.Skipped = countSkipped(res.Plan)
	res.Failed = len(res.Errors)
	for i := range res.Plan {
		if res.Plan[i].Action == "skip" && res.Plan[i].SkipReason == "" {
			res.Plan[i].SkipReason = res.Plan[i].Detail
		}
	}
}

func countSkipped(writes []PlannedWrite) int {
	skipped := 0
	for _, write := range writes {
		if write.Action == "skip" {
			skipped++
		}
	}
	return skipped
}

// deployTargetsOnce is the former DeployTargets body. Preview=true writes
// nothing; Preview=false performs the writes.
func deployTargetsOnce(repoRoot string, opts CompileOptions, tools []string) (*SyncResult, error) {
	if repoRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		repoRoot = cwd
	}

	opts.RepoRoot = repoRoot
	if opts.Stack == "" {
		opts.Stack = DetectStack(repoRoot)
	}
	res := &SyncResult{
		RepoRoot:     repoRoot,
		FilesWritten: []string{},
		Symlinks:     []string{},
		Errors:       []string{},
		Preview:      opts.Preview,
	}

	compiler := NewCompiler(repoRoot)
	compiled, err := compiler.Compile(opts)
	if err != nil {
		return nil, fmt.Errorf("compile rules: %w", err)
	}

	projectManaged, err := compiler.CompileProjectManagedBlock(opts)
	if err != nil {
		return nil, fmt.Errorf("compile project rules: %w", err)
	}

	var principlesBody string
	if opts.IncludePrinciples {
		pack := DefaultPrinciplesPack(opts.Stack)
		principlesBody = pack.RenderMarkdown()
	}

	toolsMap := make(map[string]bool)
	allTools := len(tools) == 0 || (len(tools) == 1 && tools[0] == "all")
	for _, t := range tools {
		toolsMap[strings.ToLower(t)] = true
	}

	// 1. Root AGENTS.md (always canonical): owned-block merge preserving all
	// user content outside the sila block.
	agentsPath := filepath.Join(repoRoot, "AGENTS.md")
	existing, _ := readExisting(agentsPath)
	cleanExisting := StripPrinciplesBlock(existing)

	// The fingerprint is computed from the exact body ComposeAgentsFile writes,
	// so a skip decision can be made without diffing an ephemeral whole-file
	// hash.
	expectedBody := composeManagedBody(ExtractUserContent(cleanExisting), projectManaged)
	expectedHash := HashManagedBody(expectedBody)
	_, existingMeta, existingOK, existingLegacy := ExtractManagedBlock(cleanExisting)
	existingHash := ""
	if existingOK && !existingLegacy {
		existingHash = existingMeta.Hash
	}
	hashMatch := existingHash != "" && existingMeta.Revision == ManagedRevision && existingHash == expectedHash

	merged := ComposeAgentsFile(cleanExisting, projectManaged)
	if opts.IncludePrinciples {
		merged = InjectPrinciples(merged, principlesBody)
	} else if existingPrinciples, ok := ExtractPrinciplesBlock(existing); ok {
		merged = InjectPrinciples(merged, existingPrinciples)
	}

	res.ManagedHash = expectedHash
	res.ManagedRevision = ManagedRevision

	plan := PlannedWrite{
		Path:                "AGENTS.md",
		BeforeBytes:         len(existing),
		AfterBytes:          len(merged),
		ManagedHash:         expectedHash,
		ExistingManagedHash: existingHash,
		ManagedRevision:     ManagedRevision,
		HashMatch:           hashMatch,
	}
	if len(merged) > MaxRuleFileBytes {
		errMsg := fmt.Sprintf("AGENTS.md (%d bytes) exceeds Antigravity 24 KB ceiling (%d bytes); aborting write to protect rule budget", len(merged), MaxRuleFileBytes)
		plan.Action = "error"
		plan.Detail = errMsg
		res.Plan = append(res.Plan, plan)
		res.Errors = append(res.Errors, errMsg)
	} else {
		if existing == "" {
			plan.Action = "create"
		} else {
			plan.Action = "merge"
			if !strings.Contains(existing, OwnedBlockBegin) {
				if plan.Detail != "" {
					plan.Detail += " — existing content without sila markers is preserved verbatim above the sila block"
				} else {
					plan.Detail = "existing content without sila markers is preserved verbatim above the sila block"
				}
			}
		}
		if existing != merged || opts.Force {
			if existing == merged {
				plan.Action = "repair"
				plan.Detail = "forced repair (content canonical; mode normalized)"
			}
			if hashMatch {
				if plan.Detail != "" {
					plan.Detail += " — "
				}
				plan.Detail += fmt.Sprintf("managed hash match (rev=%d); rewriting to reconcile surrounding content", ManagedRevision)
			}
			plan.Diff = unifiedDiff(existing, merged, "AGENTS.md")
			res.Plan = append(res.Plan, plan)
			if !opts.Preview {
				if err := atomicWriteFile(agentsPath, []byte(merged), 0644); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("write AGENTS.md: %v", err))
				} else {
					res.FilesWritten = append(res.FilesWritten, "AGENTS.md")
				}
			}
		} else {
			plan.Action = "skip"
			if hashMatch {
				plan.Detail = fmt.Sprintf("managed hash match (rev=%d)", ManagedRevision)
				res.HashSkips++
			} else if plan.Detail == "" {
				plan.Detail = "already up to date"
			}
			res.Plan = append(res.Plan, plan)
		}
	}

	// 2. Claude Code (CLAUDE.md): never destroy user content. A regular file
	// is left untouched unless opts.Replace authorizes replacement.
	deployClaude := toolsMap["claude"] || toolsMap["claudecode"]
	if allTools && HasClaudeIntegration(repoRoot) {
		deployClaude = true
	}
	if deployClaude {
		claudePath := filepath.Join(repoRoot, "CLAUDE.md")
		fi, statErr := os.Lstat(claudePath)
		switch {
		case os.IsNotExist(statErr):
			cplan := PlannedWrite{Path: "CLAUDE.md", Action: "symlink", Detail: "CLAUDE.md -> AGENTS.md"}
			res.Plan = append(res.Plan, cplan)
			if !opts.Preview {
				if err := atomicSymlink("AGENTS.md", claudePath); err == nil {
					res.Symlinks = append(res.Symlinks, "CLAUDE.md -> AGENTS.md")
				} else {
					res.Errors = append(res.Errors, fmt.Sprintf("symlink CLAUDE.md: %v", err))
				}
			}
		case fi.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(claudePath)
			if filepath.Clean(target) == "AGENTS.md" {
				res.Plan = append(res.Plan, PlannedWrite{Path: "CLAUDE.md", Action: "skip", Detail: "symlink to AGENTS.md already in place"})
				break
			}
			if !opts.Force {
				res.Plan = append(res.Plan, PlannedWrite{Path: "CLAUDE.md", Action: "skip", Detail: "symlink does not point to AGENTS.md; pass --force to repair"})
				break
			}
			res.Plan = append(res.Plan, PlannedWrite{Path: "CLAUDE.md", Action: "repair", Detail: "forcing CLAUDE.md -> AGENTS.md symlink"})
			if !opts.Preview {
				if err := os.Remove(claudePath); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("repair CLAUDE.md: %v", err))
					break
				}
				if err := atomicSymlink("AGENTS.md", claudePath); err == nil {
					res.Symlinks = append(res.Symlinks, "CLAUDE.md -> AGENTS.md")
				} else {
					res.Errors = append(res.Errors, fmt.Sprintf("symlink CLAUDE.md: %v", err))
				}
			}
		case opts.Replace:
			cplan := PlannedWrite{Path: "CLAUDE.md", Action: "replace", Detail: "user file replaced by CLAUDE.md -> AGENTS.md symlink (--replace)", BeforeBytes: int(fi.Size())}
			res.Plan = append(res.Plan, cplan)
			if !opts.Preview {
				if err := os.Remove(claudePath); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("replace CLAUDE.md: %v", err))
					break
				}
				if err := atomicSymlink("AGENTS.md", claudePath); err == nil {
					res.Symlinks = append(res.Symlinks, "CLAUDE.md -> AGENTS.md")
				} else {
					res.Errors = append(res.Errors, fmt.Sprintf("symlink CLAUDE.md: %v", err))
				}
			}
		default:
			if opts.IncludePrinciples {
				existingClaude, _ := readExisting(claudePath)
				updatedClaude := InjectPrinciples(existingClaude, principlesBody)
				if existingClaude == updatedClaude {
					res.Plan = append(res.Plan, PlannedWrite{
						Path:        "CLAUDE.md",
						Action:      "skip",
						Detail:      "already up to date",
						BeforeBytes: len(existingClaude),
					})
				} else {
					res.Plan = append(res.Plan, PlannedWrite{
						Path:        "CLAUDE.md",
						Action:      "merge",
						Detail:      "injected principles block into regular file (user content preserved)",
						BeforeBytes: len(existingClaude),
						AfterBytes:  len(updatedClaude),
						Diff:        unifiedDiff(existingClaude, updatedClaude, "CLAUDE.md"),
					})
					if !opts.Preview {
						if err := atomicWriteFile(claudePath, []byte(updatedClaude), 0644); err != nil {
							res.Errors = append(res.Errors, fmt.Sprintf("write CLAUDE.md: %v", err))
						} else {
							res.FilesWritten = append(res.FilesWritten, "CLAUDE.md")
						}
					}
				}
			} else {
				res.Plan = append(res.Plan, PlannedWrite{
					Path:   "CLAUDE.md",
					Action: "skip",
					Detail: "existing CLAUDE.md left untouched; pass --replace to manage it via the AGENTS.md symlink",
				})
			}
		}
	} else if allTools {
		res.Plan = append(res.Plan, PlannedWrite{
			Path:   "CLAUDE.md",
			Action: "skip",
			Detail: "no Claude harness detected; unprompted file creation skipped",
		})
	}

	// 2b. Gemini / Antigravity (GEMINI.md): symlink to AGENTS.md
	deployGemini := toolsMap["gemini"] || toolsMap["antigravity"] || toolsMap["agy"]
	if allTools && HasGeminiIntegration(repoRoot) {
		deployGemini = true
	}
	if deployGemini {
		geminiPath := filepath.Join(repoRoot, "GEMINI.md")
		fi, statErr := os.Lstat(geminiPath)
		switch {
		case os.IsNotExist(statErr):
			gplan := PlannedWrite{Path: "GEMINI.md", Action: "symlink", Detail: "GEMINI.md -> AGENTS.md"}
			res.Plan = append(res.Plan, gplan)
			if !opts.Preview {
				if err := atomicSymlink("AGENTS.md", geminiPath); err == nil {
					res.Symlinks = append(res.Symlinks, "GEMINI.md -> AGENTS.md")
				} else {
					res.Errors = append(res.Errors, fmt.Sprintf("symlink GEMINI.md: %v", err))
				}
			}
		case fi.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(geminiPath)
			if filepath.Clean(target) == "AGENTS.md" {
				res.Plan = append(res.Plan, PlannedWrite{Path: "GEMINI.md", Action: "skip", Detail: "symlink to AGENTS.md already in place"})
				break
			}
			if !opts.Force {
				res.Plan = append(res.Plan, PlannedWrite{Path: "GEMINI.md", Action: "skip", Detail: "symlink does not point to AGENTS.md; pass --force to repair"})
				break
			}
			res.Plan = append(res.Plan, PlannedWrite{Path: "GEMINI.md", Action: "repair", Detail: "forcing GEMINI.md -> AGENTS.md symlink"})
			if !opts.Preview {
				if err := os.Remove(geminiPath); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("repair GEMINI.md: %v", err))
					break
				}
				if err := atomicSymlink("AGENTS.md", geminiPath); err == nil {
					res.Symlinks = append(res.Symlinks, "GEMINI.md -> AGENTS.md")
				} else {
					res.Errors = append(res.Errors, fmt.Sprintf("symlink GEMINI.md: %v", err))
				}
			}
		case opts.Replace:
			gplan := PlannedWrite{Path: "GEMINI.md", Action: "replace", Detail: "user file replaced by GEMINI.md -> AGENTS.md symlink (--replace)", BeforeBytes: int(fi.Size())}
			res.Plan = append(res.Plan, gplan)
			if !opts.Preview {
				if err := os.Remove(geminiPath); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("replace GEMINI.md: %v", err))
					break
				}
				if err := atomicSymlink("AGENTS.md", geminiPath); err == nil {
					res.Symlinks = append(res.Symlinks, "GEMINI.md -> AGENTS.md")
				} else {
					res.Errors = append(res.Errors, fmt.Sprintf("symlink GEMINI.md: %v", err))
				}
			}
		default:
			res.Plan = append(res.Plan, PlannedWrite{
				Path:   "GEMINI.md",
				Action: "skip",
				Detail: "existing GEMINI.md left untouched; pass --replace to manage it via the AGENTS.md symlink",
			})
		}
	} else if allTools {
		res.Plan = append(res.Plan, PlannedWrite{
			Path:   "GEMINI.md",
			Action: "skip",
			Detail: "no Gemini or Antigravity harness detected; unprompted file creation skipped",
		})
	}

	// 3. Cursor Rules 2.0 (.cursor/rules/000-sila-core.mdc & 000-sila-principles.mdc)
	deployCursor := toolsMap["cursor"]
	if allTools && (HasCursorIntegration(repoRoot) || opts.IncludePrinciples) {
		deployCursor = true
	}
	if deployCursor {
		mdcPath := filepath.Join(repoRoot, ".cursor", "rules", "000-sila-core.mdc")
		existingMdc, _ := readExisting(mdcPath)
		mdcContent := fmt.Sprintf(`---
description: "Sila Core Engineering Principles and Invariants"
globs: ["*"]
alwaysApply: true
---
%s
<!-- Generated by 'sila agents sync'. Source: AGENTS.md -->
%s
%s`, OwnedBlockBegin, compiled, OwnedBlockEnd)
		foreign := existingMdc != "" && !strings.Contains(existingMdc, OwnedBlockBegin)
		switch {
		case existingMdc == mdcContent && !opts.Force:
			res.Plan = append(res.Plan, PlannedWrite{
				Path:        ".cursor/rules/000-sila-core.mdc",
				Action:      "skip",
				Detail:      "already current",
				BeforeBytes: len(existingMdc),
			})
		case foreign && !opts.Replace:
			res.Plan = append(res.Plan, PlannedWrite{
				Path:        ".cursor/rules/000-sila-core.mdc",
				Action:      "skip",
				Detail:      "existing file was not generated by sila; pass --replace to manage it",
				BeforeBytes: len(existingMdc),
			})
		default:
			action := "write"
			if existingMdc == "" {
				action = "create"
			} else if foreign {
				action = "replace"
			} else if existingMdc == mdcContent && opts.Force {
				action = "repair"
			}
			res.Plan = append(res.Plan, PlannedWrite{
				Path:        ".cursor/rules/000-sila-core.mdc",
				Action:      action,
				BeforeBytes: len(existingMdc),
				AfterBytes:  len(mdcContent),
				Diff:        unifiedDiff(existingMdc, mdcContent, ".cursor/rules/000-sila-core.mdc"),
			})
			if !opts.Preview {
				if err := os.MkdirAll(filepath.Dir(mdcPath), 0755); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("mkdir .cursor/rules: %v", err))
				} else if err := atomicWriteFile(mdcPath, []byte(mdcContent), 0644); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("write 000-sila-core.mdc: %v", err))
				} else {
					res.FilesWritten = append(res.FilesWritten, ".cursor/rules/000-sila-core.mdc")
				}
			}
		}

		if opts.IncludePrinciples {
			mdcPrinciplesPath := filepath.Join(repoRoot, ".cursor", "rules", "000-sila-principles.mdc")
			existingP, _ := readExisting(mdcPrinciplesPath)
			base := existingP
			if existingP == "" {
				base = "---\ndescription: \"Sila Universal Principles Pack and Layered Constitution\"\nglobs: [\"*\"]\nalwaysApply: true\n---\n"
			}
			targetP := InjectPrinciples(base, principlesBody)

			if existingP == targetP {
				res.Plan = append(res.Plan, PlannedWrite{
					Path:        ".cursor/rules/000-sila-principles.mdc",
					Action:      "skip",
					Detail:      "already up to date",
					BeforeBytes: len(existingP),
				})
			} else {
				action := "write"
				if existingP == "" {
					action = "create"
				} else {
					action = "merge"
				}
				res.Plan = append(res.Plan, PlannedWrite{
					Path:        ".cursor/rules/000-sila-principles.mdc",
					Action:      action,
					BeforeBytes: len(existingP),
					AfterBytes:  len(targetP),
					Diff:        unifiedDiff(existingP, targetP, ".cursor/rules/000-sila-principles.mdc"),
				})
				if !opts.Preview {
					if err := os.MkdirAll(filepath.Dir(mdcPrinciplesPath), 0755); err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("mkdir .cursor/rules: %v", err))
					} else if err := atomicWriteFile(mdcPrinciplesPath, []byte(targetP), 0644); err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("write 000-sila-principles.mdc: %v", err))
					} else {
						res.FilesWritten = append(res.FilesWritten, ".cursor/rules/000-sila-principles.mdc")
					}
				}
			}
		}
	} else if allTools {
		res.Plan = append(res.Plan, PlannedWrite{
			Path:   ".cursor/rules/000-sila-core.mdc",
			Action: "skip",
			Detail: "no Cursor harness detected; unprompted file creation skipped",
		})
	}

	// 4. Windsurf (.windsurfrules)
	if (allTools || toolsMap["windsurf"]) && opts.IncludePrinciples {
		wsPath := filepath.Join(repoRoot, ".windsurfrules")
		existingWS, _ := readExisting(wsPath)
		targetWS := InjectPrinciples(existingWS, principlesBody)

		if existingWS == targetWS {
			res.Plan = append(res.Plan, PlannedWrite{
				Path:        ".windsurfrules",
				Action:      "skip",
				Detail:      "already up to date",
				BeforeBytes: len(existingWS),
			})
		} else {
			action := "write"
			if existingWS == "" {
				action = "create"
			} else {
				action = "merge"
			}
			res.Plan = append(res.Plan, PlannedWrite{
				Path:        ".windsurfrules",
				Action:      action,
				BeforeBytes: len(existingWS),
				AfterBytes:  len(targetWS),
				Diff:        unifiedDiff(existingWS, targetWS, ".windsurfrules"),
			})
			if !opts.Preview {
				if err := atomicWriteFile(wsPath, []byte(targetWS), 0644); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("write .windsurfrules: %v", err))
				} else {
					res.FilesWritten = append(res.FilesWritten, ".windsurfrules")
				}
			}
		}
	}

	// 5. GitHub Copilot (.github/copilot-instructions.md)
	if (allTools || toolsMap["copilot"] || toolsMap["github-copilot"]) && opts.IncludePrinciples {
		cpPath := filepath.Join(repoRoot, ".github", "copilot-instructions.md")
		existingCP, _ := readExisting(cpPath)
		targetCP := InjectPrinciples(existingCP, principlesBody)

		if existingCP == targetCP {
			res.Plan = append(res.Plan, PlannedWrite{
				Path:        ".github/copilot-instructions.md",
				Action:      "skip",
				Detail:      "already up to date",
				BeforeBytes: len(existingCP),
			})
		} else {
			action := "write"
			if existingCP == "" {
				action = "create"
			} else {
				action = "merge"
			}
			res.Plan = append(res.Plan, PlannedWrite{
				Path:        ".github/copilot-instructions.md",
				Action:      action,
				BeforeBytes: len(existingCP),
				AfterBytes:  len(targetCP),
				Diff:        unifiedDiff(existingCP, targetCP, ".github/copilot-instructions.md"),
			})
			if !opts.Preview {
				if err := os.MkdirAll(filepath.Dir(cpPath), 0755); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("mkdir .github: %v", err))
				} else if err := atomicWriteFile(cpPath, []byte(targetCP), 0644); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("write copilot-instructions.md: %v", err))
				} else {
					res.FilesWritten = append(res.FilesWritten, ".github/copilot-instructions.md")
				}
			}
		}
	}

	// 6. Devin (.devin/setup.sh) — created only when absent.
	deployDevin := toolsMap["devin"]
	if allTools && HasDevinIntegration(repoRoot) {
		deployDevin = true
	}
	if deployDevin {
		setupPath := filepath.Join(repoRoot, ".devin", "setup.sh")
		if _, err := os.Stat(setupPath); os.IsNotExist(err) || opts.Force {
			script := `#!/bin/bash
# Generated by 'sila install' for Cognition Devin
set -e
echo "· [sila] verifying environment in Devin sandbox..."
if command -v sila >/dev/null 2>&1; then
    sila doctor || true
    sila prime || true
fi
`
			res.Plan = append(res.Plan, PlannedWrite{Path: ".devin/setup.sh", Action: "create"})
			if !opts.Preview {
				if err := os.MkdirAll(filepath.Dir(setupPath), 0755); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("mkdir .devin: %v", err))
				} else if err := atomicWriteFile(setupPath, []byte(script), 0755); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("write .devin/setup.sh: %v", err))
				} else {
					res.FilesWritten = append(res.FilesWritten, ".devin/setup.sh")
				}
			}
		} else {
			res.Plan = append(res.Plan, PlannedWrite{Path: ".devin/setup.sh", Action: "skip", Detail: "already present"})
		}
	} else if allTools {
		res.Plan = append(res.Plan, PlannedWrite{
			Path:   ".devin/setup.sh",
			Action: "skip",
			Detail: "no Devin harness detected; unprompted file creation skipped",
		})
	}

	return res, nil
}

type pathSnapshot struct {
	Path      string
	Exists    bool
	IsSymlink bool
	Link      string
	Data      []byte
	Mode      os.FileMode
}

const maxRollbackBytes = 8 * 1024 * 1024

func snapshotPlanPaths(repoRoot string, writes []PlannedWrite) ([]pathSnapshot, error) {
	var snapshots []pathSnapshot
	total := 0
	seen := map[string]bool{}
	for _, write := range writes {
		if write.Action == "skip" || write.Action == "error" || write.Path == "" || seen[write.Path] {
			continue
		}
		seen[write.Path] = true
		full := filepath.Join(repoRoot, filepath.FromSlash(write.Path))
		snap := pathSnapshot{Path: full}
		fi, err := os.Lstat(full)
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			snapshots = append(snapshots, snap)
			continue
		}
		if err != nil {
			return nil, err
		}
		snap.Exists = true
		snap.Mode = fi.Mode().Perm()
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(full)
			if err != nil {
				return nil, err
			}
			snap.IsSymlink = true
			snap.Link = target
		} else if fi.IsDir() {
			return nil, fmt.Errorf("cannot atomically replace directory %s", write.Path)
		} else {
			data, err := os.ReadFile(full)
			if err != nil {
				return nil, err
			}
			total += len(data)
			if total > maxRollbackBytes {
				return nil, fmt.Errorf("rollback snapshot exceeds %d bytes", maxRollbackBytes)
			}
			snap.Data = data
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, nil
}

func restoreSnapshots(snapshots []pathSnapshot) error {
	var failures []string
	for i := len(snapshots) - 1; i >= 0; i-- {
		snap := snapshots[i]
		_ = os.Remove(snap.Path)
		switch {
		case !snap.Exists:
			// Removed newly created path.
		case snap.IsSymlink:
			if err := atomicSymlink(snap.Link, snap.Path); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", snap.Path, err))
			}
		default:
			if err := atomicWriteFile(snap.Path, snap.Data, snap.Mode); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", snap.Path, err))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sila-sync-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func atomicSymlink(target, link string) error {
	tmp := link + fmt.Sprintf(".sila-tmp-%d", os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err == nil {
		return nil
	}
	// Windows cannot rename over an existing path; the commit is still
	// rollback-protected by the caller's snapshot.
	_ = os.Remove(link)
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// HasGeminiIntegration checks whether the repository uses Gemini/Antigravity.
func HasGeminiIntegration(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	if _, err := os.Lstat(filepath.Join(repoRoot, "GEMINI.md")); err == nil {
		return true
	}
	if fi, err := os.Stat(filepath.Join(repoRoot, ".gemini")); err == nil && fi.IsDir() {
		return true
	}
	return false
}

// HasClaudeIntegration checks whether the repository uses Claude Code.
func HasClaudeIntegration(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	if _, err := os.Lstat(filepath.Join(repoRoot, "CLAUDE.md")); err == nil {
		return true
	}
	if fi, err := os.Stat(filepath.Join(repoRoot, ".claude")); err == nil && fi.IsDir() {
		return true
	}
	return false
}

// HasCursorIntegration checks whether the repository uses Cursor.
func HasCursorIntegration(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	if isSilaSourceRepo(repoRoot) {
		return true
	}
	if fi, err := os.Stat(filepath.Join(repoRoot, ".cursor")); err == nil && fi.IsDir() {
		return true
	}
	return false
}

// HasDevinIntegration checks whether the repository uses Devin.
func HasDevinIntegration(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	if isSilaSourceRepo(repoRoot) {
		return true
	}
	if fi, err := os.Stat(filepath.Join(repoRoot, ".devin")); err == nil && fi.IsDir() {
		return true
	}
	return false
}
