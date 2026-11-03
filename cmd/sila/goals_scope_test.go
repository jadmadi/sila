package main

import (
	"strings"
	"testing"
)

func TestScopeFailureAction(t *testing.T) {
	cases := []struct {
		name          string
		scope         string
		hasSubcommand bool
		isGlobal      bool
		wantHard      bool
		wantNotice    string
	}{
		{
			name:          "scoped mutation is hard error",
			scope:         "mahak-benc",
			hasSubcommand: true,
			isGlobal:      false,
			wantHard:      true,
			wantNotice:    `sila goals: unknown project "mahak-benc"`,
		},
		{
			name:          "scoped mutation under global is hard error",
			scope:         "mahak-benc",
			hasSubcommand: true,
			isGlobal:      true,
			wantHard:      true,
			wantNotice:    `sila goals: unknown project "mahak-benc"`,
		},
		{
			name:          "bare positional keeps audit with notice",
			scope:         "numbered-plan-steps-and-plannotator-parity",
			hasSubcommand: false,
			isGlobal:      false,
			wantHard:      false,
			wantNotice:    `sila goals: unknown project "numbered-plan-steps-and-plannotator-parity", auditing current directory`,
		},
		{
			name:          "bare positional under global stays silent",
			scope:         "typo",
			hasSubcommand: false,
			isGlobal:      true,
			wantHard:      false,
			wantNotice:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notice, hard := scopeFailureAction(tc.scope, tc.hasSubcommand, tc.isGlobal)
			if hard != tc.wantHard {
				t.Errorf("hard = %v, expected %v", hard, tc.wantHard)
			}
			if notice != tc.wantNotice {
				t.Errorf("notice = %q, expected %q", notice, tc.wantNotice)
			}
		})
	}
}

func TestScopeFailureActionNoticeNamesScope(t *testing.T) {
	notice, hard := scopeFailureAction("sila", true, false)
	if !hard {
		t.Fatal("expected hard error for scoped subcommand")
	}
	if !strings.Contains(notice, `"sila"`) {
		t.Errorf("expected notice to name the bad scope, got %q", notice)
	}
}
