package salam

import (
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/hookconfig"
)

func TestBuildBriefReportsMemoryContractAndHookStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	brief, err := BuildBrief(nil, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if brief.MemoryContractVersion != hookconfig.MemoryContractVersion {
		t.Fatalf("memory contract = %q, want %q", brief.MemoryContractVersion, hookconfig.MemoryContractVersion)
	}
	if !brief.HookStatus["claude"].Supported {
		t.Fatalf("claude should be a supported hook target: %+v", brief.HookStatus)
	}
	if brief.HookHint == "" {
		t.Fatal("expected a hook installation hint when no hooks are installed")
	}
	if !strings.Contains(brief.Markdown, "Memory Contract") || !strings.Contains(brief.Markdown, hookconfig.MemoryContractVersion) {
		t.Fatalf("markdown missing memory contract:\n%s", brief.Markdown)
	}
}
