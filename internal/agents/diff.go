package agents

import (
	"fmt"
	"slices"
	"strings"
)

// UnifiedDiff renders a compact unified diff between two text contents using
// an LCS over lines. Instruction files are small, so the O(n*m) table is fine.
// Identical inputs return "".
func UnifiedDiff(before, after, name string) string {
	return computeUnifiedDiff(before, after, name)
}

func unifiedDiff(before, after, name string) string {
	return computeUnifiedDiff(before, after, name)
}

func computeUnifiedDiff(before, after, name string) string {
	if before == after {
		return ""
	}
	a := splitLines(before)
	b := splitLines(after)

	// LCS table
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i, v := range slices.Backward(a) {
		for j := len(b) - 1; j >= 0; j-- {
			if v == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	type op struct {
		kind byte // ' ', '-', '+'
		line string
	}
	var ops []op
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, op{'+', b[j]})
	}

	const context = 2
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", name, name)

	// Emit hunks with `context` unchanged lines of surrounding context.
	n := len(ops)
	k := 0
	for k < n {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(k-context, 0)
		// Extend through the change block plus trailing context.
		end := k
		for end < n {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := 0
			for end+run < n && ops[end+run].kind == ' ' {
				run++
			}
			if run > context*2 {
				end += context
				break
			}
			end += run
		}
		if end > n {
			end = n
		}

		// Hunk header line numbers (1-based, count only emitted lines).
		var oldStart, oldLen, newStart, newLen int
		for x := start; x < end; x++ {
			switch ops[x].kind {
			case ' ':
				oldLen++
				newLen++
				if oldStart == 0 {
					oldStart = x + 1
				}
				if newStart == 0 {
					newStart = x + 1
				}
			case '-':
				oldLen++
				if oldStart == 0 {
					oldStart = x + 1
				}
			case '+':
				newLen++
				if newStart == 0 {
					newStart = x + 1
				}
			}
		}
		// Line numbers: count of prior lines before the hunk start.
		var oldBefore, newBefore int
		for x := 0; x < start; x++ {
			switch ops[x].kind {
			case ' ', '-':
				oldBefore++
			}
			switch ops[x].kind {
			case ' ', '+':
				newBefore++
			}
		}
		oldLine := oldBefore + 1
		if oldLen == 0 {
			oldLine = 0
		}
		newLine := newBefore + 1
		if newLen == 0 {
			newLine = 0
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", oldLine, oldLen, newLine, newLen)
		for x := start; x < end; x++ {
			sb.WriteByte(ops[x].kind)
			sb.WriteString(ops[x].line)
			sb.WriteByte('\n')
		}
		k = end
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	return lines
}
