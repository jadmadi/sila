package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ValidateTaskPacket verifies that all assumptions in a TaskPacket remain valid against current repository state.
// It detects spec drift, source drift, and dependency drift without modifying the packet or repository.
func ValidateTaskPacket(rootDir string, packet *TaskPacket) []Diagnostic {
	if packet == nil {
		return []Diagnostic{
			{
				Code:        DiagPacketSpecStale,
				Severity:    "error",
				Message:     "task packet is nil",
				Remediation: "provide a non-nil TaskPacket",
			},
		}
	}

	if rootDir == "" {
		rootDir = "."
	}

	var diags []Diagnostic

	// 1. Verify WorkPackage spec digest (spec drift)
	pkg, err := LoadWorkPackage(rootDir, packet.GoalSlug)
	if err != nil {
		diags = append(diags, Diagnostic{
			Code:        DiagPacketSpecStale,
			Severity:    "error",
			GoalSlug:    packet.GoalSlug,
			Message:     fmt.Sprintf("work package for %s not found or unreadable: %v", packet.GoalSlug, err),
			Remediation: "ensure work-package.json exists and is valid",
		})
	} else {
		currentSpecDigest, digestErr := WorkPackageDigest(pkg)
		if digestErr != nil {
			diags = append(diags, Diagnostic{
				Code:        DiagPacketSpecStale,
				Severity:    "error",
				GoalSlug:    packet.GoalSlug,
				Message:     fmt.Sprintf("failed to compute work package digest: %v", digestErr),
				Remediation: "resolve syntax or formatting issues in work-package.json",
			})
		} else if currentSpecDigest != packet.SpecDigest {
			diags = append(diags, Diagnostic{
				Code:        DiagPacketSpecStale,
				Severity:    "error",
				GoalSlug:    packet.GoalSlug,
				Path:        filepath.Join("goals", packet.GoalSlug, "work-package.json"),
				Message:     fmt.Sprintf("work package specification has drifted: recorded %s, current %s", packet.SpecDigest, currentSpecDigest),
				Remediation: "re-generate task packet after reviewing specification changes",
			})
		}
	}

	// 2. Verify source pins against files on disk (source drift)
	for _, pin := range packet.SourcePins {
		fullPath := filepath.Join(rootDir, filepath.Clean(filepath.ToSlash(pin.Path)))
		if pin.MustBeAbsent {
			if fi, statErr := os.Stat(fullPath); statErr == nil && !fi.IsDir() {
				diags = append(diags, Diagnostic{
					Code:        DiagPacketSourceStale,
					Severity:    "error",
					GoalSlug:    packet.GoalSlug,
					Path:        pin.Path,
					Message:     fmt.Sprintf("pinned create path %q was expected to be absent but now exists on disk", pin.Path),
					Remediation: "remove unexpected file or re-evaluate task scope",
				})
			}
		} else {
			data, readErr := os.ReadFile(fullPath)
			if readErr != nil {
				diags = append(diags, Diagnostic{
					Code:        DiagPacketSourceStale,
					Severity:    "error",
					GoalSlug:    packet.GoalSlug,
					Path:        pin.Path,
					Message:     fmt.Sprintf("pinned source file %q is missing on disk: %v", pin.Path, readErr),
					Remediation: "restore missing source file or re-generate task packet",
				})
			} else {
				sum := sha256.Sum256(data)
				actualSHA := hex.EncodeToString(sum[:])
				if actualSHA != pin.SHA256 {
					diags = append(diags, Diagnostic{
						Code:        DiagPacketSourceStale,
						Severity:    "error",
						GoalSlug:    packet.GoalSlug,
						Path:        pin.Path,
						Message:     fmt.Sprintf("pinned source file %q has modified content: recorded %s, current %s", pin.Path, pin.SHA256, actualSHA),
						Remediation: "re-generate task packet after reviewing source changes",
					})
				}
			}
		}
	}

	// 3. Verify dependency outputs and predecessor artifacts (dependency drift)
	for _, dep := range packet.DependencyOutputs {
		if dep.Artifact == "candidate" || dep.Artifact == "candidate_digest" {
			cand, candErr := CaptureTaskCandidate(rootDir, packet.GoalSlug)
			if candErr == nil && cand != nil {
				curDigest := TaskCandidateDigest(*cand)
				if curDigest != "" && dep.SHA256 != "" && curDigest != dep.SHA256 {
					diags = append(diags, Diagnostic{
						Code:        DiagPacketDependencyStale,
						Severity:    "error",
						GoalSlug:    packet.GoalSlug,
						Path:        dep.Artifact,
						Message:     fmt.Sprintf("predecessor candidate for task %q is stale: recorded %s, current %s", dep.TaskID, dep.SHA256, curDigest),
						Remediation: "re-run predecessor task or re-align candidate digest",
					})
				}
			}
			continue
		}
		if dep.Artifact == "spec" || dep.Artifact == "spec_digest" {
			if pkg != nil {
				curSpec, curSpecErr := WorkPackageDigest(pkg)
				if curSpecErr == nil && curSpec != "" && dep.SHA256 != "" && curSpec != dep.SHA256 {
					diags = append(diags, Diagnostic{
						Code:        DiagPacketDependencyStale,
						Severity:    "error",
						GoalSlug:    packet.GoalSlug,
						Path:        dep.Artifact,
						Message:     fmt.Sprintf("predecessor spec for task %q is stale: recorded %s, current %s", dep.TaskID, dep.SHA256, curSpec),
						Remediation: "re-run predecessor task or re-align spec digest",
					})
				}
			}
			continue
		}
		if dep.Artifact != "" {
			fullPath := filepath.Join(rootDir, filepath.Clean(filepath.ToSlash(dep.Artifact)))
			data, readErr := os.ReadFile(fullPath)
			if readErr != nil {
				diags = append(diags, Diagnostic{
					Code:        DiagPacketDependencyStale,
					Severity:    "error",
					GoalSlug:    packet.GoalSlug,
					Path:        dep.Artifact,
					Message:     fmt.Sprintf("predecessor artifact %q for task %q is missing on disk", dep.Artifact, dep.TaskID),
					Remediation: "re-run predecessor task to regenerate required artifact",
				})
			} else if dep.SHA256 != "" {
				sum := sha256.Sum256(data)
				actualSHA := hex.EncodeToString(sum[:])
				if actualSHA != dep.SHA256 {
					diags = append(diags, Diagnostic{
						Code:        DiagPacketDependencyStale,
						Severity:    "error",
						GoalSlug:    packet.GoalSlug,
						Path:        dep.Artifact,
						Message:     fmt.Sprintf("predecessor artifact %q has modified content: recorded %s, current %s", dep.Artifact, dep.SHA256, actualSHA),
						Remediation: "re-run predecessor task or re-align dependency digest",
					})
				}
			}
		}
	}

	// Sort diagnostics deterministically
	sort.Slice(diags, func(i, j int) bool {
		ki := fmt.Sprintf("%s:%s:%s:%s", diags[i].Severity, diags[i].Code, diags[i].Path, diags[i].Message)
		kj := fmt.Sprintf("%s:%s:%s:%s", diags[j].Severity, diags[j].Code, diags[j].Path, diags[j].Message)
		return ki < kj
	})

	return diags
}
