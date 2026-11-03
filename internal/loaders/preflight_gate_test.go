package loaders

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePreflightFixture(t *testing.T, projectDir, sessionID, status string, created time.Time) string {
	t.Helper()
	primeStatus := PrimeStatusOK
	if status != "ok" {
		primeStatus = PrimeStatusFailed
	}
	marker := PreflightMarker{
		SchemaVersion: PreflightSchema,
		SessionID:     sessionID,
		Project:       "probe",
		ProjectDir:    projectDir,
		SilaVersion:   "test",
		CreatedAt:     created.UTC().Format(time.RFC3339),
		Policy:        PreflightEnforce,
		Status:        status,
		Checks:        map[string]bool{"sila_present": true, "prime": primeStatus == PrimeStatusOK},
		Warnings:      []string{},
		PrimeStatus:   primeStatus,
		PrimeAt:       created.UTC().Format(time.RFC3339),
	}
	marker.ContextSHA256 = PreflightContextHash("probe", sessionID, PreflightEnforce, status)
	path, err := WritePreflightMarker(projectDir, marker)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPreflightPolicyPrecedence(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	if got := ResolvePreflightPolicyWithUser(project, user); got != PreflightWarn {
		t.Fatalf("default = %q, want warn", got)
	}
	if err := os.MkdirAll(filepath.Join(user, "sila"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user, "sila", "config"), []byte("[command_code]\npreflight_policy = \"enforce\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolvePreflightPolicyWithUser(project, user); got != PreflightEnforce {
		t.Fatalf("user policy = %q, want enforce", got)
	}
	if err := os.MkdirAll(filepath.Join(project, ".sila"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".sila", "config"), []byte("[command_code]\npreflight_policy = \"off\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolvePreflightPolicyWithUser(project, user); got != PreflightOff {
		t.Fatalf("project policy = %q, want off", got)
	}
	if err := os.MkdirAll(filepath.Join(project, ".commandcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".commandcode", "settings.json"), []byte(`{"sila":{"preflight_policy":"warn"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// .sila/config outranks settings.json.
	if got := ResolvePreflightPolicyWithUser(project, user); got != PreflightOff {
		t.Fatalf("precedence policy = %q, want off", got)
	}
}

func TestPreflightMarkerRoundTripAndValidation(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	path := writePreflightFixture(t, project, "s1", "ok", now)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode = %o, want 600", info.Mode().Perm())
	}
	marker, err := ReadPreflightMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if marker.ContextSHA256 != PreflightContextHash("probe", "s1", PreflightEnforce, "ok") {
		t.Fatalf("context hash = %q", marker.ContextSHA256)
	}
	if err := ValidatePreflightMarker(path, marker, "s1", now.Add(-time.Minute), now); err != nil {
		t.Fatalf("valid marker rejected: %v", err)
	}
	if err := ValidatePreflightMarker(path, marker, "other", now.Add(-time.Minute), now); err == nil {
		t.Fatal("cross-session marker accepted")
	}
	if err := ValidatePreflightMarker(path, marker, "s1", now.Add(time.Hour), now); err == nil {
		t.Fatal("stale marker accepted")
	}
}

func TestPreflightMarkerRejectsWrongMode(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	path := writePreflightFixture(t, project, "s1", "ok", now)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	marker, _ := ReadPreflightMarker(path)
	if err := ValidatePreflightMarker(path, marker, "s1", now, now); err == nil || !strings.Contains(err.Error(), "600") {
		t.Fatalf("wrong mode error = %v", err)
	}
}

func TestPreflightMarkerRejectsSymlink(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	path := writePreflightFixture(t, project, "s1", "ok", now)
	link := filepath.Join(t.TempDir(), "marker.json")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	marker, err := ReadPreflightMarker(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePreflightMarker(link, marker, "s1", now, now); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink marker error = %v", err)
	}
}

func TestPreflightMarkerRejectsLegacyAndFailedPrime(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()

	legacy := PreflightMarker{
		SchemaVersion: 1, SessionID: "s1", Project: "probe", ProjectDir: project,
		CreatedAt: now.Format(time.RFC3339), Policy: PreflightEnforce, Status: "ok",
		Checks: map[string]bool{"sila_present": true, "prime": true},
	}
	legacyPath, err := WritePreflightMarker(project, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePreflightMarker(legacyPath, &legacy, "s1", now, now); err == nil {
		t.Fatal("legacy marker without prime_status was accepted")
	}

	degraded := PreflightMarker{
		SchemaVersion: PreflightSchema, SessionID: "s1", Project: "probe", ProjectDir: project,
		CreatedAt: now.Format(time.RFC3339), Policy: PreflightEnforce, Status: "degraded",
		Checks:      map[string]bool{"sila_present": true, "prime": false},
		PrimeStatus: PrimeStatusFailed, PrimeAt: now.Format(time.RFC3339),
	}
	degradedPath, err := WritePreflightMarker(project, degraded)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePreflightMarker(degradedPath, &degraded, "s1", now, now); err == nil {
		t.Fatal("failed prime marker was accepted")
	}
}

func TestEvaluatePreflightProjectAndTTL(t *testing.T) {
	now := time.Now().UTC()
	projectA := t.TempDir()
	projectB := t.TempDir()
	writePreflightFixture(t, projectA, "s1", "ok", now)
	mismatch := PreflightRequest{
		Policy: PreflightEnforce, SessionID: "s1", ProjectDir: projectB,
		MarkerPath: PreflightMarkerPath(projectA, "s1"),
		Now:        now, ToolName: "Read", ToolInput: `{"path":"x"}`,
	}
	if decision := EvaluatePreflight(mismatch); decision.Decision != "deny" || !strings.Contains(decision.Reason, "another project") {
		t.Fatalf("project mismatch = %+v", decision)
	}

	project := t.TempDir()
	writePreflightFixture(t, project, "s1", "ok", now.Add(-31*time.Minute))
	stale := PreflightRequest{
		Policy: PreflightEnforce, SessionID: "s1", ProjectDir: project,
		Now: now, ToolName: "Read", ToolInput: `{"path":"x"}`,
	}
	if decision := EvaluatePreflight(stale); decision.Decision != "deny" || !strings.Contains(decision.Reason, "older than") {
		t.Fatalf("stale marker = %+v", decision)
	}
}

func TestEvaluatePreflightPolicyMatrix(t *testing.T) {
	now := time.Now().UTC()
	base := PreflightRequest{
		SessionID: "s1", ProjectDir: t.TempDir(), ToolName: "Read", ToolInput: `{"path":"x"}`,
		Now: now,
	}

	off := base
	off.Policy = PreflightOff
	if decision := EvaluatePreflight(off); decision.Decision != "allow" {
		t.Fatalf("off decision = %+v", decision)
	}

	warn := base
	warn.Policy = PreflightWarn
	first := EvaluatePreflight(warn)
	if first.Decision != "warn" || first.Warning == "" {
		t.Fatalf("warn decision = %+v", first)
	}
	second := EvaluatePreflight(warn)
	if second.Decision != "warn" || second.Warning != "" {
		t.Fatalf("warn dedupe = %+v", second)
	}

	enforce := base
	enforce.Policy = PreflightEnforce
	denied := EvaluatePreflight(enforce)
	if denied.Decision != "deny" || denied.RecoveryCommand != "sila hook session-start" {
		t.Fatalf("enforce decision = %+v", denied)
	}

	writePreflightFixture(t, base.ProjectDir, "s1", "ok", now)
	if allowed := EvaluatePreflight(enforce); allowed.Decision != "allow" {
		t.Fatalf("valid marker decision = %+v", allowed)
	}
}

func TestEvaluatePreflightAllowlist(t *testing.T) {
	base := PreflightRequest{
		Policy: PreflightEnforce, SessionID: "s1", ProjectDir: t.TempDir(),
		Now: time.Now().UTC(), ToolName: "Read",
	}
	cases := []struct {
		name, tool, input string
		allowed           bool
	}{
		{"mcp", "mcp__sila__prime_context", `{}`, true},
		{"prime", "Shell", `{"command":"sila prime"}`, true},
		{"recall", "Shell", `{"command":"sila recall \"db locked\""}`, true},
		{"heal", "Shell", `{"command":"sila heal --dry-run"}`, true},
		{"preflight", "Shell", `{"command":"preflight"}`, true},
		{"read", "Read", `{"path":"secret"}`, false},
		{"write", "Write", `{"path":"x","content":"y"}`, false},
		{"shell", "Shell", `{"command":"cat ~/.ssh/id_rsa"}`, false},
		{"injection", "Shell", `{"command":"sila prime; rm -rf /"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			req.ToolName, req.ToolInput = tc.tool, tc.input
			got := EvaluatePreflight(req).Decision
			if (got == "allow") != tc.allowed {
				t.Fatalf("decision = %q, allowed=%v", got, tc.allowed)
			}
		})
	}
}

func TestEvaluatePreflightFailureModes(t *testing.T) {
	now := time.Now().UTC()
	project := t.TempDir()
	base := PreflightRequest{
		Policy: PreflightEnforce, SessionID: "s1", ProjectDir: project,
		ToolName: "Read", ToolInput: `{"path":"x"}`, Now: now, SessionStart: now.Add(-time.Minute),
	}

	corrupt := PreflightMarkerPath(project, "s1")
	if err := os.MkdirAll(filepath.Dir(corrupt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corrupt, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if decision := EvaluatePreflight(base); decision.Decision != "deny" {
		t.Fatalf("corrupt marker decision = %+v", decision)
	}

	writePreflightFixture(t, project, "s1", "failed", now)
	if decision := EvaluatePreflight(base); decision.Decision != "deny" {
		t.Fatalf("failed status decision = %+v", decision)
	}

	bypass := base
	bypass.Bypass = true
	if decision := EvaluatePreflight(bypass); decision.Decision != "allow" || decision.Warning == "" || !decision.Bypassed {
		t.Fatalf("bypass decision = %+v", decision)
	}

	plan := base
	plan.PermissionMode = "plan"
	if decision := EvaluatePreflight(plan); decision.Decision != "allow" || decision.Warning == "" {
		t.Fatalf("plan decision = %+v", decision)
	}
}

func TestPreflightMarkerPathTraversalSafe(t *testing.T) {
	path := PreflightMarkerPath("/tmp/project", "../../etc/passwd")
	expectedPrefix := filepath.Join("/tmp/project", ".sila", "state", "preflight") + string(filepath.Separator)
	if !strings.HasPrefix(path, expectedPrefix) || strings.Contains(path, string(filepath.Separator)+"etc"+string(filepath.Separator)) {
		t.Fatalf("marker path escaped: %q", path)
	}
	if !strings.HasSuffix(path, ".json") {
		t.Fatalf("marker path = %q", path)
	}
}

func BenchmarkEvaluatePreflightWarm(b *testing.B) {
	project := b.TempDir()
	now := time.Now().UTC()
	marker := PreflightMarker{
		SchemaVersion: PreflightSchema, SessionID: "s1", Project: "probe", ProjectDir: project,
		CreatedAt: now.Format(time.RFC3339), Policy: PreflightEnforce, Status: "ok",
		Checks:      map[string]bool{"sila_present": true, "prime": true},
		PrimeStatus: PrimeStatusOK, PrimeAt: now.Format(time.RFC3339),
	}
	if _, err := WritePreflightMarker(project, marker); err != nil {
		b.Fatal(err)
	}
	req := PreflightRequest{
		Policy: PreflightEnforce, SessionID: "s1", ProjectDir: project,
		ToolName: "Read", ToolInput: `{"path":"x"}`, Now: now,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if decision := EvaluatePreflight(req); decision.Decision != "allow" {
			b.Fatalf("decision = %+v", decision)
		}
	}
}
