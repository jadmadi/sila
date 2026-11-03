package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxTraceFileBytes = 1 << 20 // 1 MiB
	maxTraceLineBytes = 4 << 10 // 4 KiB
)

var traceSecretKeyRE = regexp.MustCompile(`(?i)secret|token|password|key|auth|credential`)

type traceRecord struct {
	Timestamp     string            `json:"timestamp"`
	Tool          string            `json:"tool"`
	ArgumentKeys  []string          `json:"argument_keys"`
	ArgumentTypes map[string]string `json:"argument_types"`
	RawShape      string            `json:"raw_shape"`
	DurationMS    int64             `json:"duration_ms"`
	Outcome       string            `json:"outcome"`
}

// traceToolCall appends one metadata-only line to the local MCP trace file.
// It records keys and JSON types, never values, queries, paths, or secrets.
// Every failure is silent: tracing must never fail a tool call.
func (s *Server) traceToolCall(tool string, original, normalized json.RawMessage, start time.Time, outcome string) {
	if !s.traceArgs && os.Getenv("SILA_MCP_TRACE_ARGS") != "1" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	dir := filepath.Join(home, ".local", "share", "sila")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, "mcp-trace.jsonl")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxTraceFileBytes {
		_ = os.Rename(path, path+".1")
	}

	record := traceRecord{
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Tool:          tool,
		ArgumentKeys:  []string{},
		ArgumentTypes: map[string]string{},
		RawShape:      jsonValueType(original),
		DurationMS:    time.Since(start).Milliseconds(),
		Outcome:       outcome,
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &obj); err == nil {
		for key, value := range obj {
			name := key
			if traceSecretKeyRE.MatchString(key) {
				name = "redacted"
			}
			record.ArgumentKeys = append(record.ArgumentKeys, name)
			record.ArgumentTypes[name] = jsonValueType(value)
		}
	}
	sort.Strings(record.ArgumentKeys)
	line, err := json.Marshal(record)
	if err != nil {
		return
	}
	if len(line) > maxTraceLineBytes {
		line = line[:maxTraceLineBytes]
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// traceKeySummary is used by tests to assert redaction without reading values.
func traceKeySummary(raw json.RawMessage) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		if traceSecretKeyRE.MatchString(key) {
			key = "redacted"
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
