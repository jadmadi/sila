package assist

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestInterpretActivityRecencyPreservation(t *testing.T) {
	ctx := context.Background()

	// Build 20 messages ordered newest-first (index 0 is newest)
	var msgs []store.StoredMessage
	for i := range 20 {
		msgs = append(msgs, store.StoredMessage{
			ID:          int64(i + 1),
			ProjectID:   "sila",
			Content:     fmt.Sprintf("MSG_INDEX_%02d: content of message", i),
			Role:        "assistant",
			TS:          "2026-09-18T12:00:00Z",
			SourceTools: []string{"claude-code"},
		})
	}

	// Capture evaluate call state using custom fake provider
	recProvider := &recordingProvider{
		response: &ProviderResponse{
			Nouls: map[string]NoulAnswer{
				"has_blockers": {Noul: 0.90},
			},
		},
	}

	insights := InterpretActivity(ctx, recProvider, msgs)
	if len(insights) == 0 {
		t.Fatalf("expected at least one insight from recording provider")
	}

	// Ensure the newest message (MSG_INDEX_00) was sent in evidence
	foundNewest := false
	foundOldest := false
	for _, v := range recProvider.lastState {
		dump := fmt.Sprintf("%v", v)
		if strings.Contains(dump, "MSG_INDEX_00") {
			foundNewest = true
		}
		if strings.Contains(dump, "MSG_INDEX_19") {
			foundOldest = true
		}
	}

	if !foundNewest {
		t.Errorf("expected newest message MSG_INDEX_00 to be included in evidence")
	}
	if foundOldest {
		t.Errorf("expected oldest message MSG_INDEX_19 to be omitted from 15-message window")
	}
}

func TestInterpretActivityAttributionAndStatus(t *testing.T) {
	ctx := context.Background()

	recProvider := &recordingProvider{
		response: &ProviderResponse{
			Nouls: map[string]NoulAnswer{
				"has_blockers":            {Noul: 0.88},
				"has_architecture_change": {Noul: 0.94},
				"has_security_issue":      {Noul: 0.91},
			},
		},
	}

	msgs := []store.StoredMessage{
		{
			ID:          101,
			ProjectID:   "atlas-web",
			Content:     "Fixed SQLite deadlock error by enabling WAL mode and busy_timeout = 5000",
			Role:        "assistant",
			TS:          "2026-09-18T14:30:00Z",
			SourceTools: []string{"mimocode"},
		},
		{
			ID:          102,
			ProjectID:   "atlas-web",
			Content:     "Encountered critical database locked error blocker on checkout webhook",
			Role:        "assistant",
			TS:          "2026-09-18T14:25:00Z",
			SourceTools: []string{"devin"},
		},
		{
			ID:          103,
			ProjectID:   "atlas-web",
			Content:     "Configured credential and secret token rotation for API access",
			Role:        "assistant",
			TS:          "2026-09-18T14:20:00Z",
			SourceTools: []string{"claude-code"},
		},
	}

	structured := InterpretActivityStructured(ctx, recProvider, msgs)
	if len(structured) != 3 {
		t.Fatalf("expected 3 structured insights, got %d", len(structured))
	}

	var foundBlocker, foundArch, foundSec bool
	for _, in := range structured {
		switch in.Category {
		case "blocker":
			foundBlocker = true
			if in.Tool != "devin" {
				t.Errorf("expected blocker attributed to devin, got %s", in.Tool)
			}
			if in.Status != "unresolved" {
				t.Errorf("expected blocker status unresolved, got %s", in.Status)
			}
		case "architecture":
			foundArch = true
			if in.Tool != "mimocode" {
				t.Errorf("expected architecture attributed to mimocode, got %s", in.Tool)
			}
			if in.Status != "confirmed observation" {
				t.Errorf("expected architecture status confirmed observation, got %s", in.Status)
			}
		case "security":
			foundSec = true
			if in.Tool != "claude-code" {
				t.Errorf("expected security attributed to claude-code, got %s", in.Tool)
			}
		}
	}

	if !foundBlocker || !foundArch || !foundSec {
		t.Errorf("missing expected insight categories: blocker=%v, arch=%v, sec=%v", foundBlocker, foundArch, foundSec)
	}
}

func TestInterpretActivityArabicSupport(t *testing.T) {
	ctx := context.Background()

	recProvider := &recordingProvider{
		response: &ProviderResponse{
			Nouls: map[string]NoulAnswer{
				"has_architecture_change": {Noul: 0.92},
			},
		},
	}

	msgs := []store.StoredMessage{
		{
			ID:          201,
			ProjectID:   "sila",
			Content:     "تم حل مشكلة قفل قاعدة البيانات من خلال تفعيل نمط WAL وتحديد مهلة الانتظار",
			Role:        "assistant",
			TS:          "2026-09-18T16:00:00Z",
			SourceTools: []string{"mimocode"},
		},
	}

	structured := InterpretActivityStructured(ctx, recProvider, msgs)
	if len(structured) != 1 {
		t.Fatalf("expected 1 insight for Arabic activity, got %d", len(structured))
	}

	in := structured[0]
	if in.Tool != "mimocode" {
		t.Errorf("expected tool mimocode, got %s", in.Tool)
	}
	if in.Status != "confirmed observation" {
		t.Errorf("expected status 'confirmed observation', got %s", in.Status)
	}
	if !strings.Contains(in.Attribution, "تم حل") {
		t.Errorf("expected Arabic excerpt in attribution, got %s", in.Attribution)
	}
}

func TestInterpretActivityFailOpenModes(t *testing.T) {
	ctx := context.Background()

	// 1. Nil provider returns nil
	if res := InterpretActivity(ctx, nil, []store.StoredMessage{{Content: "hello"}}); res != nil {
		t.Errorf("expected nil for nil provider, got %v", res)
	}

	// 2. Empty messages returns nil
	mock := NewMockAdapter()
	if res := InterpretActivity(ctx, mock, nil); res != nil {
		t.Errorf("expected nil for empty messages, got %v", res)
	}

	// 3. Failing provider returns nil (fail open)
	failP := &failProvider{}
	if res := InterpretActivity(ctx, failP, []store.StoredMessage{{Content: "hello"}}); res != nil {
		t.Errorf("expected nil for failing provider, got %v", res)
	}
}

type recordingProvider struct {
	response  *ProviderResponse
	lastState map[string]any
}

func (r *recordingProvider) Name() ModelProvider { return ProviderMock }
func (r *recordingProvider) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]Question) (*ProviderResponse, error) {
	r.lastState = state
	if r.response != nil {
		return r.response, nil
	}
	return &ProviderResponse{
		Provider: ProviderMock,
		Nouls:    make(map[string]NoulAnswer),
	}, nil
}

func TestInterpretActivityMultiToolAndNegation(t *testing.T) {
	ctx := context.Background()

	recProvider := &recordingProvider{
		response: &ProviderResponse{
			Nouls: map[string]NoulAnswer{
				"has_architecture_change": {Noul: 0.95},
			},
		},
	}

	msgs := []store.StoredMessage{
		{
			ID:          301,
			ProjectID:   "sila",
			Content:     "Architecture is not implemented yet across harnesses",
			Role:        "assistant",
			TS:          "2026-09-18T18:00:00Z",
			SourceTools: []string{"codex", "opencode"},
		},
	}

	structured := InterpretActivityStructured(ctx, recProvider, msgs)
	if len(structured) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(structured))
	}

	in := structured[0]
	if in.Tool != "codex, opencode" {
		t.Errorf("expected multi-tool attribution 'codex, opencode', got %q", in.Tool)
	}
	if in.Status != "unresolved" {
		t.Errorf("expected status 'unresolved' for negated architecture statement, got %q", in.Status)
	}
}
