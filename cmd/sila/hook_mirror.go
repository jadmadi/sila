package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/importmem"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

const (
	mirrorDebounce  = 2 * time.Second
	mirrorStateRel  = ".sila/state/mirror"
	mirrorDebouncef = "debounce.json"
)

var shellRedirectRE = regexp.MustCompile(`(?:^|\s)(?:>>?|tee\s+)(["']?)([^\s"';&|]+)`)

// runHookMemoryMirror mirrors allowlisted memory files written by one post-tool
// event. It never changes the hook exit code; failures log once per session.
func runHookMemoryMirror(st *store.Store, env *loaders.HookEnvelope, cwd string, jsonOut bool) {
	if st == nil || env == nil || cwd == "" {
		return
	}
	enabled, cfgErr := mirrorEnabled()
	if cfgErr != nil {
		_ = loaders.AppendHookAudit(cwd, loaders.HookAuditEntry{
			Event: "memory-mirror", SessionID: env.SessionID, Agent: env.Agent,
			Summary: "mirror disabled: invalid configuration",
		})
		return
	}
	if !enabled {
		return
	}
	home, _ := os.UserHomeDir()
	paths := mirrorPathsFromPayload(env.Payload, cwd)
	var mirrored, duplicates, failed int
	for _, path := range paths {
		if mirrorExcluded(path, env.Payload) {
			continue
		}
		target, ok := importmem.ClassifyMirrorPath(home, cwd, path)
		if !ok {
			continue
		}
		if mirrorDebounced(cwd, env.SessionID, target.Path, time.Now()) {
			continue
		}
		res, err := importmem.MirrorOne(context.Background(), st, importmem.ImportOptions{
			ProjectID:     env.ProjectID,
			RepoRoot:      cwd,
			MirrorSession: env.SessionID,
		}, target)
		if err != nil {
			failed++
			mirrorFailureOnce(cwd, env.SessionID, err)
			continue
		}
		mirrored += res.Mirrored
		duplicates += res.Duplicates
		if res.Failed > 0 {
			failed++
			mirrorFailureOnce(cwd, env.SessionID, fmt.Errorf("mirror %s", res.Code))
		}
	}
	if mirrored+duplicates > 0 {
		if jsonOut {
			emitJSON(map[string]int{"mirrored": mirrored, "duplicates": duplicates})
		} else {
			fmt.Fprintf(os.Stdout, "memory mirror: %d mirrored, %d duplicates\n", mirrored, duplicates)
		}
	}
	_ = failed
}

func mirrorEnabled() (bool, error) {
	cfg, err := config.Load("")
	if err != nil {
		return false, err
	}
	return cfg.MemoryMirrorEnabled(), nil
}

func mirrorPathsFromPayload(payload map[string]interface{}, cwd string) []string {
	if len(payload) == 0 {
		return nil
	}
	tool := strings.ToLower(payloadString(payload, "tool_name", "tool", "name"))
	input := payloadString(payload, "tool_input", "toolInput", "command", "input")
	var paths []string
	if tool == "bash" || tool == "shell" || tool == "exec" || tool == "terminal" {
		for _, match := range shellRedirectRE.FindAllStringSubmatch(input, -1) {
			paths = append(paths, mirrorAbs(cwd, match[2]))
		}
	}
	var decoded interface{}
	if json.Unmarshal([]byte(input), &decoded) == nil {
		collectMirrorPaths(decoded, cwd, &paths)
	}
	return dedupeStrings(paths)
}

func payloadString(payload map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok && value != nil {
			if text, ok := value.(string); ok {
				return text
			}
			if encoded, err := json.Marshal(value); err == nil {
				return string(encoded)
			}
		}
	}
	return ""
}

func collectMirrorPaths(value interface{}, cwd string, out *[]string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if lower == "file_path" || lower == "filepath" || lower == "path" || lower == "target_file" || lower == "file" {
				appendMirrorPath(child, cwd, out)
				continue
			}
			collectMirrorPaths(child, cwd, out)
		}
	case []interface{}:
		for _, child := range typed {
			collectMirrorPaths(child, cwd, out)
		}
	}
}

func appendMirrorPath(value interface{}, cwd string, out *[]string) {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			*out = append(*out, mirrorAbs(cwd, typed))
		}
	case []interface{}:
		for _, item := range typed {
			appendMirrorPath(item, cwd, out)
		}
	}
}

func mirrorAbs(cwd, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path)
}

func mirrorExcluded(path string, payload map[string]interface{}) bool {
	if path == "" {
		return true
	}
	clean := filepath.ToSlash(path)
	if strings.Contains(clean, "/.sila/") || strings.Contains(clean, "/.sila") {
		return true
	}
	tool := strings.ToLower(payloadString(payload, "tool_name", "tool", "name"))
	input := strings.ToLower(payloadString(payload, "tool_input", "toolInput", "command", "input"))
	if (tool == "bash" || tool == "shell" || tool == "exec") && strings.HasPrefix(strings.TrimSpace(input), "sila ") {
		return true
	}
	return false
}

// mirrorDebounced reports whether the same path was mirrored within the
// debounce window for this session, and records the attempt.
func mirrorDebounced(cwd, session, path string, now time.Time) bool {
	stateDir := filepath.Join(cwd, filepath.FromSlash(mirrorStateRel))
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return false
	}
	file := filepath.Join(stateDir, mirrorDebouncef)
	seen := map[string]int64{}
	if data, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(data, &seen)
	}
	key := debounceKey(session, path)
	last := time.UnixMilli(seen[key])
	if !last.IsZero() && now.Sub(last) < mirrorDebounce {
		return true
	}
	seen[key] = now.UnixMilli()
	cutoff := now.Add(-time.Minute).UnixMilli()
	for k, ts := range seen {
		if ts < cutoff {
			delete(seen, k)
		}
	}
	if encoded, err := json.Marshal(seen); err == nil {
		_ = os.WriteFile(file, encoded, 0o600)
	}
	return false
}

func debounceKey(session, path string) string {
	sum := sha256.Sum256([]byte(session + "\x00" + path))
	return hex.EncodeToString(sum[:])[:24]
}

func mirrorFailureOnce(cwd, session string, err error) {
	stateDir := filepath.Join(cwd, filepath.FromSlash(mirrorStateRel))
	_ = os.MkdirAll(stateDir, 0o700)
	marker := filepath.Join(stateDir, "error-"+debounceKey(session, "session")+".flag")
	if _, statErr := os.Stat(marker); statErr == nil {
		return
	}
	_ = os.WriteFile(marker, []byte("1"), 0o600)
	fmt.Fprintf(os.Stderr, "sila hook: memory mirror: %v\n", err)
}
