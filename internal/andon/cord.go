package andon

import (
	"fmt"
	"sync"
)

type rollbackItem struct {
	name string
	fn   func() error
}

// Cord coordinates invariant checking, rollback registration, and immediate abort execution.
type Cord struct {
	mu            sync.Mutex
	operation     string
	rollbacks     []rollbackItem
	preconditions []string
	failedPrecond []string
	pulled        bool
	halt          *HaltError
}

// NewCord initializes an Andon cord for a given high-stakes operation.
func NewCord(operation string) *Cord {
	return &Cord{
		operation: operation,
		rollbacks: make([]rollbackItem, 0, 4),
	}
}

// Operation returns the name of the protected operation.
func (c *Cord) Operation() string {
	return c.operation
}

// RegisterRollback registers a rollback function to be executed in LIFO order if the cord is pulled.
func (c *Cord) RegisterRollback(name string, fn func() error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pulled {
		return
	}
	c.rollbacks = append(c.rollbacks, rollbackItem{name: name, fn: fn})
}

// CheckPrecondition checks a precondition before mutation. If check returns an error, the failure is recorded.
func (c *Cord) CheckPrecondition(name string, check func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.preconditions = append(c.preconditions, name)
	if err := check(); err != nil {
		c.failedPrecond = append(c.failedPrecond, fmt.Sprintf("%s: %v", name, err))
		return err
	}
	return nil
}

// Pull pulls the Andon cord, executing all registered rollbacks in LIFO order and generating a HaltError.
func (c *Cord) Pull(code, invariant, reason, remediation string, severity Severity, cause error, extraContext map[string]any) *HaltError {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pulled && c.halt != nil {
		return c.halt
	}
	c.pulled = true

	rollbackErrors := make([]string, 0)
	rollbacksRun := 0

	// Execute rollbacks in LIFO order
	for i := len(c.rollbacks) - 1; i >= 0; i-- {
		item := c.rollbacks[i]
		rollbacksRun++
		if err := item.fn(); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("rollback %q failed: %v", item.name, err))
		}
	}

	dump := CaptureDump(c.operation, c.preconditions, c.failedPrecond, nil, extraContext)

	halt := &HaltError{
		Code:              code,
		Operation:         c.operation,
		Severity:          severity,
		Reason:            reason,
		Invariant:         invariant,
		Remediation:       remediation,
		Context:           extraContext,
		Dump:              dump,
		RollbacksExecuted: rollbacksRun,
		RollbackErrors:    rollbackErrors,
		Cause:             cause,
	}

	c.halt = halt
	return halt
}

// Guard executes fn under panic protection. If fn panics, the cord is pulled immediately.
func (c *Cord) Guard(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			panicErr := fmt.Errorf("panic caught: %v", r)
			err = c.Pull(
				CodeRollbackPanic,
				"Zero unhandled panics during state mutations",
				fmt.Sprintf("Panic occurred during %s: %v", c.operation, r),
				"Review stack trace, revert the breaking change, and ensure defensive nil/state checks.",
				SeverityFatal,
				panicErr,
				nil,
			)
		}
	}()
	return fn()
}
