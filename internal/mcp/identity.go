package mcp

import (
	"os"
	"strings"
	"unicode"

	"github.com/jadmadi/sila/internal/config"
)

// NormalizeAgentName lowercases, removes everything that is not a letter or
// digit, and truncates to 32 runes. Punctuation-only names become empty.
func NormalizeAgentName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	normalized := b.String()
	runes := []rune(normalized)
	if len(runes) > 32 {
		return string(runes[:32])
	}
	return normalized
}

// ResolveAgent applies the documented precedence: SILA_AGENT, then agent.identity
// from config, then the clientInfo name. Empty everywhere stays empty.
func ResolveAgent(envValue, configValue, clientName string) string {
	for _, candidate := range []string{envValue, configValue, clientName} {
		if normalized := NormalizeAgentName(candidate); normalized != "" {
			return normalized
		}
	}
	return ""
}

func configuredAgentIdentity() string {
	cfg, err := config.Load("")
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.Agent.Identity
}

// effectiveAgent returns the resolved identity used for ledger rows and guards.
func (s *Server) effectiveAgent() string {
	if s == nil {
		return ""
	}
	if s.agent != "" {
		return s.agent
	}
	s.agent = ResolveAgent(os.Getenv("SILA_AGENT"), configuredAgentIdentity(), s.clientName)
	return s.agent
}

// ledgerAgent is the attribution written to the tool ledger.
func (s *Server) ledgerAgent() string {
	if agent := s.effectiveAgent(); agent != "" {
		return agent
	}
	return "anonymous"
}

// isAgentWriteTool reports whether a tool writes knowledge and therefore needs
// a non-anonymous identity.
func isAgentWriteTool(name string) bool {
	switch name {
	case "save_memo", "save_lesson", "save_handoff", "save_case", "save_memory":
		return true
	}
	return false
}
