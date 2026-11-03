package loaders

import "time"

// PrimeStatus is the terminal status of one session-start prime attempt.
type PrimeStatus string

const (
	PrimeStatusOK          PrimeStatus = "ok"
	PrimeStatusFailed      PrimeStatus = "failed"
	PrimeStatusSkipped     PrimeStatus = "skipped"
	PrimeStatusUnsupported PrimeStatus = "unsupported"
	PrimeStatusBypassed    PrimeStatus = "bypassed"
)

// PrimeSource identifies what triggered a prime attempt.
type PrimeSource string

const (
	PrimeSourceSessionStart PrimeSource = "session_start"
	PrimeSourceSubagent     PrimeSource = "subagent"
	PrimeSourceManual       PrimeSource = "manual"
	PrimeSourceUnknown      PrimeSource = "unknown"
)

// PrimeReceipt is the truthful result of one prime attempt. It is deliberately
// independent of the brief payload so the hook can record a failure even when
// BuildSessionStartBrief returns an error.
type PrimeReceipt struct {
	SessionID         string
	Status            PrimeStatus
	ErrorClass        string
	Error             string
	StartedAt         time.Time
	DurationMS        int64
	BriefBytes        int
	MemoryUnavailable bool
	Source            PrimeSource
	ContractVersion   string
}

// IsOK reports whether the receipt proves a successful prime. A degraded memory
// state (MemoryUnavailable) is not a successful prime for gate purposes.
func (r PrimeReceipt) IsOK() bool {
	return r.Status == PrimeStatusOK && !r.MemoryUnavailable
}
