package agents

import (
	"strings"
)

const (
	// StateBlockBegin and StateBlockEnd delimit the optional continuity state
	// block inside AGENTS.md. It is independent of the core owned block.
	StateBlockBegin = "<!-- sila:state:begin -->"
	StateBlockEnd   = "<!-- sila:state:end -->"

	// ManagedScopeState marks metadata owned by the state block.
	ManagedScopeState = "state"

	// StateMetaPrefix is deliberately different from OwnedBlockMetaPrefix so
	// the core composer preserves the state metadata as user content.
	StateMetaPrefix = "<!-- sila:state-meta"
	StateMetaSuffix = " -->"
)

// ComposeStateBlock writes body into a state block inside existing content.
// It preserves every byte outside the state block, is idempotent through a body
// hash, and removes the block when body is empty. changed reports whether the
// returned content differs from existing.
func ComposeStateBlock(existing, body string) (string, bool) {
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	rest, oldBody, hasOld := stripStateBlock(existing)
	if body == "" {
		if !hasOld {
			return existing, false
		}
		return rest, true
	}
	hash := HashManagedBody(body)
	if hasOld && HashManagedBody(oldBody) == hash {
		return existing, false
	}
	meta := StateMetaPrefix + " rev=1 scope=" + ManagedScopeState + " hash=" + hash + StateMetaSuffix
	block := StateBlockBegin + "\n" + meta + "\n" + body + "\n" + StateBlockEnd

	rest = strings.TrimRight(rest, "\n")
	if coreIdx := strings.Index(rest, OwnedBlockBegin); coreIdx >= 0 {
		before := strings.TrimRight(rest[:coreIdx], "\n")
		after := strings.TrimLeft(rest[coreIdx:], "\n")
		out := block + "\n\n" + after
		if before != "" {
			out = before + "\n\n" + out
		}
		return out + "\n", true
	}
	if rest == "" {
		return block + "\n", true
	}
	return rest + "\n\n" + block + "\n", true
}

// ExtractStateBlock returns the body, hash, and presence of a state block.
func ExtractStateBlock(content string) (body, hash string, ok bool) {
	_, body, ok = stripStateBlock(content)
	if !ok {
		return "", "", false
	}
	return body, HashManagedBody(body), true
}

func stripStateBlock(content string) (rest, body string, ok bool) {
	begin := strings.Index(content, StateBlockBegin)
	if begin < 0 {
		return content, "", false
	}
	afterBegin := strings.Index(content[begin:], "\n")
	if afterBegin < 0 {
		return content, "", false
	}
	bodyStart := begin + afterBegin + 1
	endRel := strings.Index(content[bodyStart:], StateBlockEnd)
	if endRel < 0 {
		return content, "", false
	}
	end := bodyStart + endRel
	body = strings.TrimSuffix(content[bodyStart:end], "\n")
	// Drop a metadata line from the extracted body.
	if idx := strings.IndexByte(body, '\n'); idx >= 0 {
		if _, valid := parseStateMeta(body[:idx]); valid {
			body = body[idx+1:]
		}
	}
	rest = content[:begin] + content[end+len(StateBlockEnd):]
	return strings.TrimRight(rest, "\n"), strings.TrimRight(body, "\n"), true
}

func parseStateMeta(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, StateMetaPrefix) || !strings.HasSuffix(trimmed, StateMetaSuffix) {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(trimmed, StateMetaPrefix), StateMetaSuffix)
	rev, scope, hash := 0, "", ""
	for _, field := range strings.Fields(inner) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return "", false
		}
		switch key {
		case "rev":
			if value != "1" {
				return "", false
			}
			rev = 1
		case "scope":
			scope = value
		case "hash":
			hash = value
		}
	}
	if rev != 1 || scope != ManagedScopeState || !isManagedHash(hash) {
		return "", false
	}
	return hash, true
}
