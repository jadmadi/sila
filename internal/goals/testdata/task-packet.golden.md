# Task: T-001 Assemble Engine Kernel

## Context
- Goal: sample-engine-delivery
- Purpose: Deliver high-performance execution packets
- Context: Initialize engine data structures with UTF-8 support (صِلَة).
- Why: Foundational kernel initialization layer.
- Prerequisites: none
- Spec Digest: f8d3c1b4e2a56789012345678901234567890123456789012345678901234567
- Packet Digest: e5f6789012345678901234567890123456789012345678901234567890123456
- Candidate Commit: 0123456789abcdef0123456789abcdef01234567
- Executable: true

## Files
- Edit: internal/kernel/engine.go
- Create: internal/kernel/output.go
- Read: docs/spec.md
- Forbid: research/*, .git/*

### Source Pins
- docs/spec.md: a1b2c3d4e5f67890123456789012345678901234567890123456789012345678
- internal/kernel/engine.go: b2c3d4e5f6789012345678901234567890123456789012345678901234567890
- internal/kernel/output.go: (must be absent)

### References
- docs/spec.md (symbol: EngineSpec) [sha256: a1b2c3d4e5f67890123456789012345678901234567890123456789012345678]:
````
```go
type KernelConfig struct {
	TickRate int // `json:"tick_rate"`
}
```
````

## Requirements
### Steps
1. **S-001**: Instantiate kernel configuration
   - Expected: TickRate configured to 60 Hz
   - On failure: Abort initialization

### Decisions
- **D-001**: Use 64-bit integer ticks
  - Rationale: Avoid integer overflow after 24 hours of operation
  - Rejected: 32-bit ticks, floating point time

### Examples
- Input: `Start()` -> Output: `nil`
  Explanation: Clean kernel startup without errors

## Interfaces
- `type EngineKernel interface { Start() error }`

## Constraints
### Task Constraints
- CGO_ENABLED=0
- Zero heap allocation in Start loop

### Global Constraints
- pure-Go stdlib
- zero external dependencies

### Non-Goals
- no GUI dashboard
- no telemetry collection

### Stop Conditions
- Two consecutive test failures with identical error
- Deadlock detected during kernel initialization

## Acceptance criteria
- **AC-001**: Kernel initializes within 10ms
  - Given: Clean operating environment
  - Action: Invoke Start()
  - Expected: Startup completed in <10ms
  - Scope: unit (mandatory: true)
  - Checks:
    - **C-001**: `go test -v ./internal/kernel` (cwd: `cli`, expected exit: 0)
- **AC-002**: Fault injection triggers expected exit code 2
  - Given: Corrupted configuration input
  - Action: Invoke StartWithCorruptedConfig()
  - Expected: Process halts with exit code 2
  - Scope: integration (mandatory: true)
  - Checks:
    - **C-002**: `go test -run TestFaultInjection` (cwd: `cli`, expected exit: 2)

## If blocked
Stop affected work and report evidence and the decision needed. Do not guess.

## Return
Schema: sila/task-result/v1
