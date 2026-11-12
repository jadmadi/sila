package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RecallGatePolicy is the opt-in policy for the pre-tool recall gate.
type RecallGatePolicy string

const (
	RecallGateOff     RecallGatePolicy = "off"
	RecallGateWarn    RecallGatePolicy = "warn"
	RecallGateEnforce RecallGatePolicy = "enforce"
)

// RecallGatePolicyError reports an invalid recall-gate policy value and where
// it came from. Environment values are diagnostic-only and degrade to warn;
// explicit flag and config values are rejected by command surfaces.
type RecallGatePolicyError struct {
	Source string
	Value  string
}

func (e *RecallGatePolicyError) Error() string {
	return fmt.Sprintf("invalid recall gate policy %q from %s (valid: off, warn, enforce)", e.Value, e.Source)
}

// DiagnosticOnly reports whether the resolver already degraded the value to
// warn, so callers must emit a diagnostic instead of failing the command.
func (e *RecallGatePolicyError) DiagnosticOnly() bool {
	return e != nil && e.Source == "environment"
}

// ParseRecallGatePolicy validates one explicit policy value.
func ParseRecallGatePolicy(raw string) (RecallGatePolicy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off":
		return RecallGateOff, nil
	case "warn":
		return RecallGateWarn, nil
	case "enforce":
		return RecallGateEnforce, nil
	default:
		return "", &RecallGatePolicyError{Source: "value", Value: raw}
	}
}

// ResolveRecallGatePolicy resolves the effective pre-tool recall policy.
// Precedence: environment > explicit flag > project config > global config >
// warn. An invalid environment value degrades to warn and returns a
// diagnostic-only error. Invalid explicit flags or config values return an
// error with the offending source.
func ResolveRecallGatePolicy(flag, projectDir, home string, env map[string]string) (RecallGatePolicy, error) {
	if env != nil {
		if raw, ok := env["SILA_RECALL_GATE"]; ok && strings.TrimSpace(raw) != "" {
			policy, err := ParseRecallGatePolicy(raw)
			if err != nil {
				return RecallGateWarn, &RecallGatePolicyError{Source: "environment", Value: raw}
			}
			return policy, nil
		}
	}
	if strings.TrimSpace(flag) != "" {
		policy, err := ParseRecallGatePolicy(flag)
		if err != nil {
			return "", &RecallGatePolicyError{Source: "flag", Value: flag}
		}
		return policy, nil
	}
	if path := FindProjectConfig(projectDir); path != "" {
		raw, ok, err := readRecallGateConfig(path)
		if err != nil {
			return "", err
		}
		if ok {
			policy, perr := ParseRecallGatePolicy(raw)
			if perr != nil {
				return "", &RecallGatePolicyError{Source: "project config", Value: raw}
			}
			return policy, nil
		}
	}
	if path := recallGateGlobalConfigPath(home, env); path != "" {
		raw, ok, err := readRecallGateConfig(path)
		if err != nil {
			return "", err
		}
		if ok {
			policy, perr := ParseRecallGatePolicy(raw)
			if perr != nil {
				return "", &RecallGatePolicyError{Source: "global config", Value: raw}
			}
			return policy, nil
		}
	}
	return RecallGateWarn, nil
}

// recallGateGlobalConfigPath resolves the global config file without relying on
// process environment, so callers can supply a test home directory.
func recallGateGlobalConfigPath(home string, env map[string]string) string {
	if env != nil {
		if xdg := strings.TrimSpace(env["XDG_CONFIG_HOME"]); xdg != "" {
			return filepath.Join(xdg, "sila", "config.json")
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	primary := filepath.Join(home, ".config", "sila", "config.json")
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	legacy := filepath.Join(home, ".local", "share", "sila", "config.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return primary
}

func readRecallGateConfig(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading config %s: %w", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", false, fmt.Errorf("parsing config %s: %w", path, err)
	}
	value, ok := raw["recall_gate_policy"]
	if !ok {
		return "", false, nil
	}
	var policy string
	if err := json.Unmarshal(value, &policy); err != nil {
		return "", false, fmt.Errorf("parsing config %s: recall_gate_policy must be a string", path)
	}
	if strings.TrimSpace(policy) == "" {
		return "", false, nil
	}
	return policy, true, nil
}
