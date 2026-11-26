package agents

import (
	"os"
	"path/filepath"
)

const (
	agentsInstructionFile = "AGENTS.md"
	claudeInstructionFile = "CLAUDE.md"
)

// EnsureInstructionAlias links the missing instruction name to the file that
// already exists. It creates nothing when both names exist, neither exists, or
// the present path is already a symlink to the missing name. A regular file is
// never replaced. The returned description is empty when nothing changed.
func EnsureInstructionAlias(repoRoot string) (string, error) {
	linkName, target, desc, ok := instructionAlias(repoRoot)
	if !ok {
		return "", nil
	}
	if err := atomicSymlink(target, filepath.Join(repoRoot, linkName)); err != nil {
		return "", err
	}
	return desc, nil
}

// instructionAlias reports the missing name and the relative target. ok is
// false when no link should be created.
func instructionAlias(repoRoot string) (linkName, target, desc string, ok bool) {
	if repoRoot == "" {
		return "", "", "", false
	}
	agentsPath := filepath.Join(repoRoot, agentsInstructionFile)
	claudePath := filepath.Join(repoRoot, claudeInstructionFile)
	agentsInfo, agentsErr := os.Lstat(agentsPath)
	claudeInfo, claudeErr := os.Lstat(claudePath)
	agentsExists := agentsErr == nil
	claudeExists := claudeErr == nil
	if agentsExists == claudeExists {
		return "", "", "", false
	}
	if agentsExists {
		if !aliasableInstruction(agentsPath, agentsInfo, claudeInstructionFile) {
			return "", "", "", false
		}
		return claudeInstructionFile, agentsInstructionFile, "CLAUDE.md -> AGENTS.md", true
	}
	if !aliasableInstruction(claudePath, claudeInfo, agentsInstructionFile) {
		return "", "", "", false
	}
	return agentsInstructionFile, claudeInstructionFile, "AGENTS.md -> CLAUDE.md", true
}

// aliasableInstruction reports whether path can be the target of a sibling
// symlink. A symlink that already names the missing file is refused so the
// pair cannot form a cycle.
func aliasableInstruction(path string, fi os.FileInfo, missingName string) bool {
	if fi == nil || fi.IsDir() {
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return false
		}
		cleaned := filepath.Clean(target)
		if cleaned == missingName || cleaned == "."+string(filepath.Separator)+missingName {
			return false
		}
		return true
	}
	return fi.Mode().IsRegular()
}
