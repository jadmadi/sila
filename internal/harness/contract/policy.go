package contract

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Policy controls whether contract failures block.
type Policy string

const (
	PolicyOff     Policy = "off"
	PolicyWarn    Policy = "warn"
	PolicyEnforce Policy = "enforce"
)

// PolicySource records where the effective policy came from.
type PolicySource string

const (
	SourceDefault PolicySource = "default"
	SourceProject PolicySource = "project"
	SourceUser    PolicySource = "user"
	SourceEnv     PolicySource = "env"
)

// EffectivePolicy is the resolved policy and its provenance.
type EffectivePolicy struct {
	Policy Policy       `json:"policy"`
	Source PolicySource `json:"source"`
	Bypass bool         `json:"bypass,omitempty"`
}

// ResolvePolicy resolves project > user > default, then the environment
// override. It never fails; malformed values fall back to the next source.
func ResolvePolicy(projectDir string) EffectivePolicy {
	effective := EffectivePolicy{Policy: PolicyWarn, Source: SourceDefault}
	if projectDir != "" {
		if p, ok := readPolicyFile(filepath.Join(projectDir, ".sila", "config")); ok {
			effective = EffectivePolicy{Policy: p, Source: SourceProject}
		}
	}
	if effective.Source == SourceDefault {
		if home, err := os.UserHomeDir(); err == nil {
			if p, ok := readPolicyFile(filepath.Join(home, ".sila", "config")); ok {
				effective = EffectivePolicy{Policy: p, Source: SourceUser}
			}
		}
	}
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("SILA_GOAL_CONTRACT"))); raw != "" {
		if p, ok := parsePolicy(raw); ok {
			effective = EffectivePolicy{Policy: p, Source: SourceEnv}
		}
	}
	if effective.Policy == PolicyEnforce && os.Getenv("SILA_GOAL_CONTRACT_BYPASS") == "1" {
		effective.Bypass = true
	}
	return effective
}

func readPolicyFile(path string) (Policy, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	inSection := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.ToLower(strings.Trim(line, "[] "))
			inSection = section == "harness.goal_contract" || section == "harness.goal-contract" || section == "goal_contract"
			continue
		}
		if !inSection {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.ToLower(strings.TrimSpace(key)) != "policy" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if p, ok := parsePolicy(value); ok {
			return p, true
		}
	}
	return "", false
}

func stripComment(s string) string {
	if i := strings.IndexAny(s, "#;"); i >= 0 {
		return s[:i]
	}
	return s
}

func parsePolicy(raw string) (Policy, bool) {
	switch Policy(strings.ToLower(strings.TrimSpace(raw))) {
	case PolicyOff:
		return PolicyOff, true
	case PolicyWarn:
		return PolicyWarn, true
	case PolicyEnforce:
		return PolicyEnforce, true
	default:
		return "", false
	}
}
