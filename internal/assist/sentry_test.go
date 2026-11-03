package assist_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/testutil"
)

type sentryMockProvider struct {
	stateReceived map[string]any
	leaksNoul     float64
	skipsNoul     float64
	schemaNoul    float64
	missingAnswer bool
	err           error
}

func (s *sentryMockProvider) Name() assist.ModelProvider {
	return assist.ProviderMock
}

func (s *sentryMockProvider) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]assist.Question) (*assist.ProviderResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.stateReceived = state
	if s.missingAnswer {
		return &assist.ProviderResponse{
			Nouls:      map[string]assist.NoulAnswer{},
			DurationMs: 25,
		}, nil
	}
	return &assist.ProviderResponse{
		Nouls: map[string]assist.NoulAnswer{
			"leaks_secret":       {Noul: s.leaksNoul},
			"skips_tests":        {Noul: s.skipsNoul},
			"destructive_schema": {Noul: s.schemaNoul},
		},
		DurationMs: 45,
	}, nil
}

func TestAuditDiff_CleanPass(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.01,
		skipsNoul:  0.02,
		schemaNoul: 0.05,
	}

	diff := "diff --git a/pkg/code.go b/pkg/code.go\n+func SafeHelper() {}\n"
	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("res: %+v", res)

	if res.Status != "pass" {
		t.Errorf("expected status 'pass', got %q", res.Status)
	}
	if res.Truncated {
		t.Errorf("expected Truncated=false for short diff")
	}
	if res.FilesCount != 1 {
		t.Errorf("expected FilesCount=1, got %d", res.FilesCount)
	}
	if res.Warning != "" {
		t.Errorf("expected empty warning on clean pass, got %q", res.Warning)
	}
}

func TestAuditDiff_SecretRedactionBeforeSubmission(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.95,
		skipsNoul:  0.01,
		schemaNoul: 0.01,
	}

	secretToken := "ghp_1234567890abcdef1234567890abcdef1234"
	diff := fmt.Sprintf("diff --git a/config.go b/config.go\n+token := %q\n", secretToken)

	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Result flags finding
	if res.Status != "finding" {
		t.Errorf("expected status 'finding', got %q", res.Status)
	}
	if !strings.Contains(res.Warning, "potential credential/secret leak detected") {
		t.Errorf("expected leak warning, got %q", res.Warning)
	}

	// 2. Secret MUST be redacted in state sent to provider
	submittedDiff, _ := mock.stateReceived["staged_diff"].(string)
	if strings.Contains(submittedDiff, secretToken) {
		t.Fatalf("SECRET LEAK TO HOSTED PROVIDER! Unredacted secret was submitted: %s", submittedDiff)
	}
	if !strings.Contains(submittedDiff, "[REDACTED_SECRET]") {
		t.Fatalf("expected [REDACTED_SECRET] placeholder in submitted diff, got: %s", submittedDiff)
	}
}

func TestAuditDiff_TailRiskDetectionBeyond8000Bytes(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.05,
		skipsNoul:  0.05,
		schemaNoul: 0.05,
	}

	// Create long diff > 8000 bytes with tail risk at byte 8500
	padding := strings.Repeat("+ regular harmless change line\n", 300)
	tailRisk := "+ " + "DROP " + "TABLE users CASCADE;\n"
	diff := "diff --git a/db/migration.sql b/db/migration.sql\n" + padding + tailRisk

	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}

	if !res.Truncated {
		t.Errorf("expected Truncated=true for >8000 byte diff")
	}
	if res.CoveredBytes != 8000 {
		t.Errorf("expected CoveredBytes=8000, got %d", res.CoveredBytes)
	}
	if res.TotalBytes <= 8000 {
		t.Errorf("expected TotalBytes > 8000, got %d", res.TotalBytes)
	}

	// Tail risk must be caught by local scan
	if !res.TailRiskDetected {
		t.Errorf("expected TailRiskDetected=true")
	}
	if res.Status != "finding" {
		t.Errorf("expected status 'finding', got %q", res.Status)
	}
	if !strings.Contains(res.Warning, "destructive database schema change") {
		t.Errorf("expected schema warning, got %q", res.Warning)
	}
	if !strings.Contains(res.Warning, "risk confirmed in diff tail") {
		t.Errorf("expected tail risk confirmation in warning, got %q", res.Warning)
	}
}

func TestAuditDiff_PartialCoverageWithoutTailRisk(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.01,
		skipsNoul:  0.01,
		schemaNoul: 0.01,
	}

	// Create long clean diff > 8000 bytes with no risk
	padding := strings.Repeat("+ harmless clean code addition\n", 350)
	diff := "diff --git a/clean.go b/clean.go\n" + padding

	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}

	if !res.Truncated {
		t.Errorf("expected Truncated=true")
	}
	if res.Status != "partial" {
		t.Errorf("expected status 'partial' for clean truncated diff, got %q", res.Status)
	}
	if !strings.Contains(res.Warning, "partial coverage") {
		t.Errorf("expected partial coverage warning, got %q", res.Warning)
	}
}

func TestAuditDiff_MissingAnswersReportedUnavailable(t *testing.T) {
	mock := &sentryMockProvider{
		missingAnswer: true,
	}

	diff := "diff --git a/a.go b/a.go\n+func A() {}\n"
	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}

	if res.Status != "unavailable" {
		t.Errorf("expected status 'unavailable' for missing answers, got %q", res.Status)
	}
	if !strings.Contains(res.Warning, "missing required answers") {
		t.Errorf("expected missing answers warning, got %q", res.Warning)
	}
}

func TestAuditDiff_PEMRedactionFullBlock(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.01,
		skipsNoul:  0.01,
		schemaNoul: 0.01,
	}

	pem := "+" + "-----BEGIN " + "PRIVATE KEY-----\n+" + "SYNTHETIC_PRIVATE_BODY_XYZ123\n+" + "-----END " + "PRIVATE KEY-----\n"
	res, err := assist.AuditDiff(context.Background(), mock, pem)
	if err != nil {
		t.Fatal(err)
	}

	submitted, _ := mock.stateReceived["staged_diff"].(string)
	if strings.Contains(submitted, "SYNTHETIC_PRIVATE_BODY_XYZ123") {
		t.Fatalf("private key body leaked to provider: %s", submitted)
	}
	if res.Status != "finding" {
		t.Errorf("expected status 'finding' for PEM private key, got %q", res.Status)
	}
}

func TestAuditDiff_RemovingDestructiveSQLDoesNotTriggerFinding(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.01,
		skipsNoul:  0.01,
		schemaNoul: 0.01,
	}

	removed := "diff --git a/db.sql b/db.sql\n--- a/db.sql\n+++ b/db.sql\n@@ -1 +1 @@\n-" + "DROP " + "TABLE users;\n+SELECT 1;\n"
	res, err := assist.AuditDiff(context.Background(), mock, removed)
	if err != nil {
		t.Fatal(err)
	}

	if res.Status != "pass" {
		t.Errorf("expected status 'pass' when removing destructive SQL, got %q (warning: %s)", res.Status, res.Warning)
	}
}

func TestAuditDiff_KnownLocalRiskWhenProviderFails(t *testing.T) {
	failingMock := &sentryMockProvider{
		err: fmt.Errorf("network offline"),
	}

	// 1. Destructive SQL added: provider fails, but local risk must be preserved
	diff := "+" + "DROP " + "TABLE users;\n"
	res, err := assist.AuditDiff(context.Background(), failingMock, diff)
	if err != nil {
		t.Fatal(err)
	}

	if res.Status != "finding" {
		t.Errorf("expected status 'finding' when local risk is confirmed despite provider failure, got %q", res.Status)
	}
	if res.DestructiveSchema != 1.0 {
		t.Errorf("expected DestructiveSchema=1.0, got %v", res.DestructiveSchema)
	}

	// 2. Clean diff: provider fails, returns unavailable
	cleanDiff := "+func Clean() {}\n"
	resClean, err := assist.AuditDiff(context.Background(), failingMock, cleanDiff)
	if err != nil {
		t.Fatal(err)
	}
	if resClean.Status != "unavailable" {
		t.Errorf("expected status 'unavailable' for clean diff when provider fails, got %q", resClean.Status)
	}
}

func TestIsTestPath(t *testing.T) {
	tests := []struct {
		path   string
		isTest bool
	}{
		{"cmd/sila/main.go", false},
		{"internal/assist/sentry.go", false},
		{"config/secrets.yaml", false},
		{"internal/assist/sentry_test.go", true},
		{"pkg/client/client_test.go", true},
		{"web/src/app.test.ts", true},
		{"web/src/app.spec.tsx", true},
		{"tests/test_auth.py", true},
		{"testdata/keys/mock.pem", true},
		{"internal/testutil/credentials.go", true},
		{"fixtures/test_db.sql", true},
		{"mocks/mock_service.go", true},
		{"", false},
	}

	for _, tc := range tests {
		got := assist.IsTestPath(tc.path)
		if got != tc.isTest {
			t.Errorf("IsTestPath(%q) = %v, expected %v", tc.path, got, tc.isTest)
		}
	}
}

func TestAuditDiff_TestFileSecretExemption(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.01,
		skipsNoul:  0.01,
		schemaNoul: 0.01,
	}

	// Secret inside a Go test file
	diff := fmt.Sprintf("diff --git a/pkg/auth_test.go b/pkg/auth_test.go\n+mockToken := %q\n+mockPEM := %q\n",
		testutil.MockToken("github"),
		testutil.MockPEMKey(),
	)

	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}

	if res.Status != "pass" {
		t.Errorf("expected status 'pass' for test file fixture, got %q (warning: %s)", res.Status, res.Warning)
	}
	if res.TestFixturesExempt == 0 {
		t.Errorf("expected TestFixturesExempt > 0, got %d", res.TestFixturesExempt)
	}
	if res.LeaksSecret >= 0.80 {
		t.Errorf("expected LeaksSecret < 0.80, got %v", res.LeaksSecret)
	}
}

func TestAuditDiff_TestdataFileExemption(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.01,
		skipsNoul:  0.01,
		schemaNoul: 0.01,
	}

	// Test fixture in testdata directory
	diff := "diff --git a/testdata/certs/test_key.pem b/testdata/certs/test_key.pem\n+" +
		"-----BEGIN RSA PRIVATE KEY-----\n+" +
		"MOCKKEYDATA00000000000000000000000000000000\n+" +
		"-----END RSA PRIVATE KEY-----\n"

	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}

	if res.Status != "pass" {
		t.Errorf("expected status 'pass' for testdata directory fixture, got %q (warning: %s)", res.Status, res.Warning)
	}
	if res.TestFixturesExempt == 0 {
		t.Errorf("expected TestFixturesExempt > 0, got %d", res.TestFixturesExempt)
	}
}

func TestAuditDiff_ProductionFileSecretBlocking(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.01, // Mock provider says low, but local sentry must force fail-closed finding
		skipsNoul:  0.01,
		schemaNoul: 0.01,
	}

	// Real secret in production code
	diff := "diff --git a/internal/config/config.go b/internal/config/config.go\n+" +
		"prodKey := \"ghp_1234567890abcdef1234567890abcdef1234\"\n"

	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}

	if res.Status != "finding" {
		t.Errorf("expected status 'finding' for production secret leak, got %q", res.Status)
	}
	if res.LeaksSecret != 1.0 {
		t.Errorf("expected LeaksSecret=1.0, got %v", res.LeaksSecret)
	}
	if !strings.Contains(res.Warning, "potential credential/secret leak detected") {
		t.Errorf("expected leak warning, got %q", res.Warning)
	}
}

func TestAuditDiff_MixedProductionAndTestDiff(t *testing.T) {
	mock := &sentryMockProvider{
		leaksNoul:  0.01,
		skipsNoul:  0.01,
		schemaNoul: 0.01,
	}

	// Mixed diff: production file has secret, test file has test token
	diff := "diff --git a/internal/config/config.go b/internal/config/config.go\n+" +
		"prodKey := \"ghp_1234567890abcdef1234567890abcdef1234\"\n" +
		"diff --git a/internal/config/config_test.go b/internal/config/config_test.go\n+" +
		fmt.Sprintf("mockKey := %q\n", testutil.MockToken("github"))

	res, err := assist.AuditDiff(context.Background(), mock, diff)
	if err != nil {
		t.Fatal(err)
	}

	if res.Status != "finding" {
		t.Errorf("expected status 'finding' when production file leaks despite test fixture, got %q", res.Status)
	}
	if res.LeaksSecret != 1.0 {
		t.Errorf("expected LeaksSecret=1.0, got %v", res.LeaksSecret)
	}
	if res.TestFixturesExempt == 0 {
		t.Errorf("expected TestFixturesExempt >= 1 for test file, got %d", res.TestFixturesExempt)
	}
}

func TestAuditMCPCall_LowRiskTool_Skipped(t *testing.T) {
	ctx := context.Background()
	audit := assist.AuditMCPCall(ctx, "search_knowledge", []byte(`{"query":"test"}`), "", nil)
	if audit.IsHighBlastRadius {
		t.Errorf("expected IsHighBlastRadius=false, got true")
	}
	if !audit.Allowed {
		t.Errorf("expected Allowed=true, got false")
	}
	if audit.Provider != "none" {
		t.Errorf("expected Provider=none, got %q", audit.Provider)
	}
	if audit.AdvisoryWarning != "" {
		t.Errorf("expected empty AdvisoryWarning, got %q", audit.AdvisoryWarning)
	}
}

func TestAuditMCPCall_HighBlastRadius_Clean(t *testing.T) {
	ctx := context.Background()
	audit := assist.AuditMCPCall(ctx, "save_memo", []byte(`{"content":"Architecture decision: use SQLite"}`), "", nil)
	if !audit.IsHighBlastRadius {
		t.Errorf("expected IsHighBlastRadius=true, got false")
	}
	if !audit.Allowed {
		t.Errorf("expected Allowed=true, got false")
	}
	if audit.AdvisoryWarning != "" {
		t.Errorf("expected empty AdvisoryWarning, got %q", audit.AdvisoryWarning)
	}
}

func TestAuditMCPCall_LocalSecretDetection(t *testing.T) {
	ctx := context.Background()
	raw := []byte(`{"content":"Leaking secret: sk-1234567890abcdef1234567890abcdef"}`)
	audit := assist.AuditMCPCall(ctx, "save_memo", raw, "", nil)
	if !audit.IsHighBlastRadius {
		t.Errorf("expected IsHighBlastRadius=true")
	}
	if !audit.Allowed {
		t.Errorf("expected Allowed=true (fail-open)")
	}
	if audit.LeaksSecret < 0.80 {
		t.Errorf("expected LeaksSecret >= 0.80, got %v", audit.LeaksSecret)
	}
	if !strings.Contains(audit.AdvisoryWarning, "potential credential exposure detected") {
		t.Errorf("expected credential advisory warning, got %q", audit.AdvisoryWarning)
	}
}

func TestAuditMCPCall_LocalDestructiveSQL(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		payload string
	}{
		{"drop table", `{"content":"DROP TABLE facts;"}`},
		{"truncate table", `{"content":"TRUNCATE TABLE messages"}`},
		{"unbounded delete", `{"content":"DELETE FROM facts;"}`},
		{"delete where 1=1", `{"content":"DELETE FROM facts WHERE 1=1"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			audit := assist.AuditMCPCall(ctx, "save_memo", []byte(tc.payload), "", nil)
			if !audit.Allowed {
				t.Errorf("expected Allowed=true (fail-open)")
			}
			if audit.DestructiveSQL < 0.80 {
				t.Errorf("expected DestructiveSQL >= 0.80, got %v", audit.DestructiveSQL)
			}
			if !strings.Contains(audit.AdvisoryWarning, "destructive database schema change or table wipe detected") {
				t.Errorf("expected destructive SQL advisory warning, got %q", audit.AdvisoryWarning)
			}
		})
	}
}

type mcpSentryMockProvider struct {
	receivedState map[string]any
	leaksNoul     float64
	schemaNoul    float64
	delay         time.Duration
}

func (m *mcpSentryMockProvider) Name() assist.ModelProvider {
	return assist.ProviderMock
}

func (m *mcpSentryMockProvider) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]assist.Question) (*assist.ProviderResponse, error) {
	m.receivedState = state
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &assist.ProviderResponse{
		Nouls: map[string]assist.NoulAnswer{
			"leaks_secret":    {Noul: m.leaksNoul},
			"destructive_sql": {Noul: m.schemaNoul},
		},
		DurationMs: 15,
	}, nil
}

func TestAuditMCPCall_JevEvaluation_And_Redaction(t *testing.T) {
	ctx := context.Background()
	mock := &mcpSentryMockProvider{
		leaksNoul:  0.95,
		schemaNoul: 0.10,
	}

	// Payload contains real secret
	raw := []byte(`{"content":"Use secret ghp_1234567890abcdef1234567890abcdef1234 for auth"}`)
	audit := assist.AuditMCPCall(ctx, "save_doc", raw, "test-proj", mock)

	if !audit.Allowed {
		t.Errorf("expected Allowed=true (fail-open)")
	}
	if audit.Provider != "jev" {
		t.Errorf("expected Provider=jev, got %q", audit.Provider)
	}

	// Verify privacy & redaction boundary: the mock received REDACTED content, not the raw secret!
	argsSent, ok := mock.receivedState["arguments"].(string)
	if !ok {
		t.Fatalf("expected arguments string in received state, got %+v", mock.receivedState)
	}
	if strings.Contains(argsSent, "ghp_1234567890abcdef1234567890abcdef1234") {
		t.Errorf("PRIVACY VIOLATION: raw secret was transmitted to JEV provider: %s", argsSent)
	}
	if !strings.Contains(argsSent, "[REDACTED_SECRET]") {
		t.Errorf("expected [REDACTED_SECRET] placeholder in arguments sent to provider: %s", argsSent)
	}
}

func TestAuditMCPCall_TimeoutFailOpen(t *testing.T) {
	ctx := context.Background()
	mock := &mcpSentryMockProvider{
		delay: 400 * time.Millisecond, // Exceeds 250ms timeout
	}

	start := time.Now()
	audit := assist.AuditMCPCall(ctx, "compile_rules", []byte(`{"clean":"true"}`), "", mock)
	elapsed := time.Since(start)

	if !audit.Allowed {
		t.Errorf("expected Allowed=true (fail-open)")
	}
	if elapsed > 350*time.Millisecond {
		t.Errorf("expected sentry timeout under 350ms, took %v", elapsed)
	}
}
