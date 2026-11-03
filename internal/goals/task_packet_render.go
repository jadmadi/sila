package goals

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RenderTaskPacketMarkdown formats a TaskPacket into a deterministic, self-contained Markdown execution contract.
func RenderTaskPacketMarkdown(packet *TaskPacket) (string, error) {
	if packet == nil {
		return "", fmt.Errorf("task packet is nil")
	}

	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("# Task: %s %s\n\n", packet.Task.ID, packet.Task.Title))

	// Context
	sb.WriteString("## Context\n")
	sb.WriteString(fmt.Sprintf("- Goal: %s\n", packet.GoalSlug))
	if packet.Purpose != "" {
		sb.WriteString(fmt.Sprintf("- Purpose: %s\n", packet.Purpose))
	}
	if packet.Task.Context != "" {
		sb.WriteString(fmt.Sprintf("- Context: %s\n", packet.Task.Context))
	}
	if packet.Task.Why != "" {
		sb.WriteString(fmt.Sprintf("- Why: %s\n", packet.Task.Why))
	}
	if len(packet.Task.DependsOn) > 0 {
		sb.WriteString(fmt.Sprintf("- Prerequisites: %s\n", strings.Join(packet.Task.DependsOn, ", ")))
	} else {
		sb.WriteString("- Prerequisites: none\n")
	}
	if packet.SpecDigest != "" {
		sb.WriteString(fmt.Sprintf("- Spec Digest: %s\n", packet.SpecDigest))
	}
	if packet.PacketDigest != "" {
		sb.WriteString(fmt.Sprintf("- Packet Digest: %s\n", packet.PacketDigest))
	}
	if packet.CandidateCommit != "" {
		sb.WriteString(fmt.Sprintf("- Candidate Commit: %s\n", packet.CandidateCommit))
	}
	sb.WriteString(fmt.Sprintf("- Executable: %v\n", packet.Executable))
	sb.WriteString("\n")

	// Files
	sb.WriteString("## Files\n")
	if len(packet.Task.Files.Edit) > 0 {
		sb.WriteString(fmt.Sprintf("- Edit: %s\n", strings.Join(packet.Task.Files.Edit, ", ")))
	}
	if len(packet.Task.Files.Create) > 0 {
		sb.WriteString(fmt.Sprintf("- Create: %s\n", strings.Join(packet.Task.Files.Create, ", ")))
	}
	if len(packet.Task.Files.Read) > 0 {
		sb.WriteString(fmt.Sprintf("- Read: %s\n", strings.Join(packet.Task.Files.Read, ", ")))
	}
	if len(packet.Task.Files.Forbid) > 0 {
		sb.WriteString(fmt.Sprintf("- Forbid: %s\n", strings.Join(packet.Task.Files.Forbid, ", ")))
	}

	if len(packet.SourcePins) > 0 {
		sb.WriteString("\n### Source Pins\n")
		for _, pin := range packet.SourcePins {
			if pin.MustBeAbsent {
				sb.WriteString(fmt.Sprintf("- %s: (must be absent)\n", pin.Path))
			} else {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", pin.Path, pin.SHA256))
			}
		}
	}

	if len(packet.Task.References) > 0 {
		sb.WriteString("\n### References\n")
		for _, ref := range packet.Task.References {
			symInfo := ""
			if ref.Symbol != "" {
				symInfo = fmt.Sprintf(" (symbol: %s)", ref.Symbol)
			}
			shaInfo := ""
			if ref.SHA256 != "" {
				shaInfo = fmt.Sprintf(" [sha256: %s]", ref.SHA256)
			}
			sb.WriteString(fmt.Sprintf("- %s%s%s:\n", ref.Path, symInfo, shaInfo))
			if ref.Excerpt != "" {
				sb.WriteString("````\n")
				sb.WriteString(ref.Excerpt)
				if !strings.HasSuffix(ref.Excerpt, "\n") {
					sb.WriteString("\n")
				}
				sb.WriteString("````\n")
			}
		}
	}
	sb.WriteString("\n")

	// Requirements
	sb.WriteString("## Requirements\n")
	if len(packet.Task.Steps) > 0 {
		sb.WriteString("### Steps\n")
		for _, step := range packet.Task.Steps {
			sb.WriteString(fmt.Sprintf("1. **%s**: %s\n", step.ID, step.Action))
			if step.Expected != "" {
				sb.WriteString(fmt.Sprintf("   - Expected: %s\n", step.Expected))
			}
			if step.OnFailure != "" {
				sb.WriteString(fmt.Sprintf("   - On failure: %s\n", step.OnFailure))
			}
		}
		sb.WriteString("\n")
	}

	if len(packet.Task.Decisions) > 0 {
		sb.WriteString("### Decisions\n")
		for _, dec := range packet.Task.Decisions {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", dec.ID, dec.Chosen))
			if dec.Rationale != "" {
				sb.WriteString(fmt.Sprintf("  - Rationale: %s\n", dec.Rationale))
			}
			if len(dec.Rejected) > 0 {
				sb.WriteString(fmt.Sprintf("  - Rejected: %s\n", strings.Join(dec.Rejected, ", ")))
			}
		}
		sb.WriteString("\n")
	}

	if len(packet.Task.Examples) > 0 {
		sb.WriteString("### Examples\n")
		for _, ex := range packet.Task.Examples {
			sb.WriteString(fmt.Sprintf("- Input: `%s` -> Output: `%s`\n", ex.Input, ex.Output))
			if ex.Explanation != "" {
				sb.WriteString(fmt.Sprintf("  Explanation: %s\n", ex.Explanation))
			}
		}
		sb.WriteString("\n")
	}

	// Interfaces
	sb.WriteString("## Interfaces\n")
	if len(packet.Task.Interfaces) > 0 {
		for _, iface := range packet.Task.Interfaces {
			sb.WriteString(fmt.Sprintf("- `%s`\n", iface))
		}
	} else {
		sb.WriteString("None declared.\n")
	}
	sb.WriteString("\n")

	// Constraints
	sb.WriteString("## Constraints\n")
	if len(packet.Task.Constraints) > 0 {
		sb.WriteString("### Task Constraints\n")
		for _, c := range packet.Task.Constraints {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}
	if len(packet.GlobalConstraints) > 0 {
		sb.WriteString("### Global Constraints\n")
		for _, c := range packet.GlobalConstraints {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}
	if len(packet.NonGoals) > 0 {
		sb.WriteString("### Non-Goals\n")
		for _, ng := range packet.NonGoals {
			sb.WriteString(fmt.Sprintf("- %s\n", ng))
		}
		sb.WriteString("\n")
	}
	if len(packet.Task.StopConditions) > 0 {
		sb.WriteString("### Stop Conditions\n")
		for _, sc := range packet.Task.StopConditions {
			sb.WriteString(fmt.Sprintf("- %s\n", sc))
		}
		sb.WriteString("\n")
	}

	// Acceptance criteria
	sb.WriteString("## Acceptance criteria\n")
	if len(packet.Acceptance) > 0 {
		// Map checks by check ID for detail rendering
		checksMap := make(map[string]Check)
		for _, ch := range packet.Task.Checks {
			checksMap[ch.ID] = ch
		}

		for _, ac := range packet.Acceptance {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", ac.ID, ac.Requirement))
			if ac.Given != "" {
				sb.WriteString(fmt.Sprintf("  - Given: %s\n", ac.Given))
			}
			if ac.Action != "" {
				sb.WriteString(fmt.Sprintf("  - Action: %s\n", ac.Action))
			}
			if ac.Expected != "" {
				sb.WriteString(fmt.Sprintf("  - Expected: %s\n", ac.Expected))
			}
			sb.WriteString(fmt.Sprintf("  - Scope: %s (mandatory: %v)\n", ac.RequiredScope, ac.Mandatory))
			if len(ac.CheckIDs) > 0 {
				sb.WriteString("  - Checks:\n")
				for _, cid := range ac.CheckIDs {
					if ch, found := checksMap[cid]; found {
						cmdStr := ch.Executable
						if len(ch.Args) > 0 {
							cmdStr += " " + strings.Join(ch.Args, " ")
						}
						sb.WriteString(fmt.Sprintf("    - **%s**: `%s` (cwd: `%s`, expected exit: %d)\n",
							ch.ID, cmdStr, ch.Cwd, ch.ExpectedExitCode))
					} else {
						sb.WriteString(fmt.Sprintf("    - **%s**\n", cid))
					}
				}
			}
		}
	} else {
		sb.WriteString("No acceptance cases assigned.\n")
	}
	sb.WriteString("\n")

	// If blocked
	sb.WriteString("## If blocked\n")
	sb.WriteString("Stop affected work and report evidence and the decision needed. Do not guess.\n\n")

	// Return
	sb.WriteString("## Return\n")
	returnFmt := packet.Task.ReturnFormat
	if returnFmt == "" {
		returnFmt = "sila/task-result/v1"
	}
	sb.WriteString(fmt.Sprintf("Schema: %s\n", returnFmt))

	result := sb.String()

	// Check max_bytes limit
	if packet.MaxBytes > 0 && len(result) > packet.MaxBytes {
		return "", fmt.Errorf("%s: rendered Markdown (%d bytes) exceeds max_bytes limit of %d bytes",
			DiagPacketTooLarge, len(result), packet.MaxBytes)
	}

	return result, nil
}

// EncodeTaskPacketJSON losslessly serializes a TaskPacket to indented JSON.
// It verifies that configured max_bytes is respected without truncation.
func EncodeTaskPacketJSON(packet *TaskPacket) ([]byte, error) {
	if packet == nil {
		return nil, fmt.Errorf("task packet is nil")
	}

	data, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal task packet json: %w", err)
	}

	if packet.MaxBytes > 0 && len(data) > packet.MaxBytes {
		return nil, fmt.Errorf("%s: encoded JSON (%d bytes) exceeds max_bytes limit of %d bytes",
			DiagPacketTooLarge, len(data), packet.MaxBytes)
	}

	return data, nil
}
