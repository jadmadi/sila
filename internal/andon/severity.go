package andon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Severity indicates the operational gravity of an Andon cord pull.
type Severity int

const (
	// SeverityAdvisory warns of degraded invariants that did not require halting.
	SeverityAdvisory Severity = iota
	// SeverityCritical halts the immediate mutation and triggers a rollback.
	SeverityCritical
	// SeverityFatal halts entire subsystem execution, requires immediate human intervention.
	SeverityFatal
)

// String returns the uppercase string representation of the severity.
func (s Severity) String() string {
	switch s {
	case SeverityAdvisory:
		return "ADVISORY"
	case SeverityCritical:
		return "CRITICAL"
	case SeverityFatal:
		return "FATAL"
	default:
		return fmt.Sprintf("SEVERITY(%d)", int(s))
	}
}

// MarshalJSON implements json.Marshaler.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *Severity) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	switch strings.ToUpper(strings.TrimSpace(str)) {
	case "ADVISORY":
		*s = SeverityAdvisory
	case "CRITICAL":
		*s = SeverityCritical
	case "FATAL":
		*s = SeverityFatal
	default:
		return fmt.Errorf("unknown andon severity: %q", str)
	}
	return nil
}

// IsFatal returns true if the severity is fatal.
func (s Severity) IsFatal() bool {
	return s == SeverityFatal
}

// IsCritical returns true if the severity is critical or fatal.
func (s Severity) IsCritical() bool {
	return s >= SeverityCritical
}
