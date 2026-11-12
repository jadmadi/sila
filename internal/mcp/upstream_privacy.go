package mcp

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/gateway"
)

var (
	egressHomeUnixRE    = regexp.MustCompile(`/(?:home|Users)/[a-zA-Z0-9_\.\-]+`)
	egressHomeWindowsRE = regexp.MustCompile(`[a-zA-Z]:\\(?:[Uu]sers)\\[a-zA-Z0-9_\.\-]+`)
	egressGHTokenRE     = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[a-zA-Z0-9]{36}\b|\bgithub_pat_[a-zA-Z0-9_]{82}\b`)
	egressBearerRE      = regexp.MustCompile(`(?i)\b(Bearer\s+)[a-zA-Z0-9_\-\.]{20,}\b`)
	egressAPIKeyRE      = regexp.MustCompile(`\b(?:sk-[a-zA-Z0-9]{20,})\b`)
	egressPemKeyRE      = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----|(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	egressParamSecretRE = regexp.MustCompile(`(?i)(password|token|secret|api_key|client_secret)=([^&\s]+)`)
	egressGLTokenRE     = regexp.MustCompile(`(?i)\b(glpat-[0-9a-zA-Z\-_]{20})\b`)
	egressSlackTokenRE  = regexp.MustCompile(`(?i)\b(xox[baprs]-[0-9a-zA-Z]{3,48})\b`)
	egressKVSecretRE    = regexp.MustCompile(`(?i)\b((?:api[_-]?key|secret[_-]?key)\s*[:=]\s*["'][0-9a-zA-Z\-_]{16,}["'])`)
)

// ContainsSensitiveEgressData reports whether text contains sensitive credentials, private keys, or local home paths.
func ContainsSensitiveEgressData(text string) bool {
	if text == "" {
		return false
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" && h != "/" {
		if strings.Contains(text, h) {
			return true
		}
	}
	return egressHomeUnixRE.MatchString(text) ||
		egressHomeWindowsRE.MatchString(text) ||
		egressGHTokenRE.MatchString(text) ||
		egressBearerRE.MatchString(text) ||
		egressAPIKeyRE.MatchString(text) ||
		egressPemKeyRE.MatchString(text) ||
		egressParamSecretRE.MatchString(text) ||
		egressGLTokenRE.MatchString(text) ||
		egressSlackTokenRE.MatchString(text) ||
		egressKVSecretRE.MatchString(text)
}

// RedactEgressString sanitizes sensitive credentials, private keys, and user home paths from outgoing text.
func RedactEgressString(text string) string {
	if text == "" {
		return ""
	}
	text = gateway.Redact(text)
	text = egressGLTokenRE.ReplaceAllString(text, "[REDACTED_SECRET]")
	text = egressSlackTokenRE.ReplaceAllString(text, "[REDACTED_SECRET]")
	text = egressKVSecretRE.ReplaceAllString(text, "$1=[REDACTED]")
	return text
}

// RedactEgressError redacts sensitive credentials and local paths from error messages.
func RedactEgressError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	clean := RedactEgressString(msg)
	if clean == msg {
		return err
	}
	return errors.New(clean)
}

// ValidateStdioArgs verifies that child-process arguments do not expose credentials or local home paths in argv.
func ValidateStdioArgs(args []string) error {
	for i, arg := range args {
		argLower := strings.ToLower(arg)
		if strings.HasPrefix(argLower, "--header=") || strings.HasPrefix(argLower, "-h=") {
			val := arg[strings.Index(arg, "=")+1:]
			if isSecretBearingArg(val) {
				return errors.New("secret-bearing --header flag forbidden in child-process arguments (argv); pass credentials via environment variables or transport headers")
			}
		}
		if (arg == "--header" || arg == "-H" || argLower == "--header" || argLower == "-h") && i+1 < len(args) {
			val := args[i+1]
			if isSecretBearingArg(val) {
				return errors.New("secret-bearing --header flag forbidden in child-process arguments (argv); pass credentials via environment variables or transport headers")
			}
		}
		if ContainsSensitiveEgressData(arg) {
			return fmt.Errorf("sensitive credential or local home path forbidden in child-process argument (argv): %s", RedactEgressString(arg))
		}
	}
	return nil
}

func isSecretBearingArg(val string) bool {
	if ContainsSensitiveEgressData(val) {
		return true
	}
	valLower := strings.ToLower(val)
	return strings.Contains(valLower, "bearer") ||
		strings.Contains(valLower, "token") ||
		strings.Contains(valLower, "secret") ||
		strings.Contains(valLower, "apikey") ||
		strings.Contains(valLower, "api-key") ||
		strings.Contains(valLower, "api_key") ||
		strings.Contains(valLower, "authorization")
}

func isExecutableParam(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "code", "script", "command", "sql", "source", "source_code", "raw_code", "patch", "diff", "eval", "expression":
		return true
	default:
		return false
	}
}

// SanitizeEgressArgs applies Sila's audited outbound privacy policy to tool call arguments.
// Non-executable arguments (prompts, questions, descriptions) have sensitive data safely scrubbed.
// Executable arguments (code, scripts, commands) are denied before transport with 0 side effects
// unless explicitly opted into via allow_sensitive=true or opt_in_privacy=true.
func SanitizeEgressArgs(toolName string, args map[string]any) (map[string]any, error) {
	if args == nil {
		return nil, nil
	}

	optIn := false
	if v, ok := args["allow_sensitive"].(bool); ok && v {
		optIn = true
	} else if v, ok := args["opt_in_privacy"].(bool); ok && v {
		optIn = true
	}

	cleaned := make(map[string]any, len(args))
	for k, v := range args {
		if k == "allow_sensitive" || k == "opt_in_privacy" {
			continue
		}
		isExec := isExecutableParam(k)
		sanitized, err := sanitizeEgressValue(toolName, k, v, isExec, optIn)
		if err != nil {
			return nil, err
		}
		cleaned[k] = sanitized
	}

	return cleaned, nil
}

func sanitizeEgressValue(toolName, paramName string, val any, isExec, optIn bool) (any, error) {
	switch v := val.(type) {
	case string:
		if ContainsSensitiveEgressData(v) {
			if isExec {
				if optIn {
					return v, nil
				}
				return nil, fmt.Errorf("upstream tool %q argument %q contains sensitive credentials or local home paths. Rewriting executable code is disabled for safety. Pass allow_sensitive=true to explicitly permit this call", toolName, paramName)
			}
			return RedactEgressString(v), nil
		}
		return v, nil

	case map[string]any:
		res := make(map[string]any, len(v))
		for subK, subV := range v {
			subExec := isExec || isExecutableParam(subK)
			sanitized, err := sanitizeEgressValue(toolName, subK, subV, subExec, optIn)
			if err != nil {
				return nil, err
			}
			res[subK] = sanitized
		}
		return res, nil

	case []any:
		res := make([]any, len(v))
		for i, item := range v {
			sanitized, err := sanitizeEgressValue(toolName, fmt.Sprintf("%s[%d]", paramName, i), item, isExec, optIn)
			if err != nil {
				return nil, err
			}
			res[i] = sanitized
		}
		return res, nil

	case []string:
		res := make([]string, len(v))
		for i, item := range v {
			if ContainsSensitiveEgressData(item) {
				if isExec {
					if optIn {
						res[i] = item
						continue
					}
					return nil, fmt.Errorf("upstream tool %q argument %q[%d] contains sensitive credentials or local home paths. Rewriting executable code is disabled for safety. Pass allow_sensitive=true to explicitly permit this call", toolName, paramName, i)
				}
				res[i] = RedactEgressString(item)
			} else {
				res[i] = item
			}
		}
		return res, nil

	default:
		return val, nil
	}
}
