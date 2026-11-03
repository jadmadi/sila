package goals

import (
	"testing"
)

func TestGoalStateValidity(t *testing.T) {
	states := []GoalState{
		StateDraft,
		StateSpecAudited,
		StateClaimed,
		StatePendingAudit,
		StateImplemented,
		StateRemediation,
		StateTainted,
		StateSuperseded,
	}

	for _, s := range states {
		if !s.IsValid() {
			t.Errorf("expected state %s to be valid", s)
		}
	}

	invalidStates := []GoalState{
		"",
		"unknown",
		"pending",
		"closed",
	}

	for _, s := range invalidStates {
		if s.IsValid() {
			t.Errorf("expected state %s to be invalid", s)
		}
	}
}

func TestValidateStateTransition_Allowed(t *testing.T) {
	allowed := [][2]GoalState{
		{StateDraft, StateDraft},
		{StateDraft, StateSpecAudited},
		{StateSpecAudited, StateClaimed},
		{StateClaimed, StateSpecAudited},
		{StateClaimed, StatePendingAudit},
		{StateClaimed, StateRemediation},
		{StatePendingAudit, StateImplemented},
		{StatePendingAudit, StateRemediation},
		{StateImplemented, StateRemediation},
		{StateRemediation, StateClaimed},
		{StateTainted, StatePendingAudit},
		{StateTainted, StateRemediation},
		// Superseded transitions from non-terminal states
		{StateDraft, StateSuperseded},
		{StateSpecAudited, StateSuperseded},
		{StateClaimed, StateSuperseded},
		{StatePendingAudit, StateSuperseded},
		{StateImplemented, StateSuperseded},
		{StateRemediation, StateSuperseded},
		{StateTainted, StateSuperseded},
	}

	for _, tr := range allowed {
		if err := ValidateStateTransition(tr[0], tr[1]); err != nil {
			t.Errorf("expected transition %s -> %s to be allowed, got error: %v", tr[0], tr[1], err)
		}
	}
}

func TestValidateStateTransition_Forbidden(t *testing.T) {
	forbidden := [][2]GoalState{
		{StateDraft, StateImplemented},       // Cannot jump straight from draft to implemented
		{StateDraft, StatePendingAudit},      // Cannot jump straight from draft to audit
		{StateSpecAudited, StateImplemented}, // Cannot implement without claiming
		{StateSuperseded, StateDraft},        // Terminal state cannot exit
		{StateSuperseded, StateClaimed},      // Terminal state cannot exit
		{StateImplemented, StateDraft},       // Cannot downgrade implemented to draft
		{StateImplemented, StateClaimed},     // Must go through remediation first
		{StateRemediation, StateImplemented}, // Must go through claim -> pending_audit -> implemented
	}

	for _, tr := range forbidden {
		if err := ValidateStateTransition(tr[0], tr[1]); err == nil {
			t.Errorf("expected transition %s -> %s to be forbidden, but it was allowed", tr[0], tr[1])
		}
	}
}
