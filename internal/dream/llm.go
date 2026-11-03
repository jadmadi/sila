package dream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// LLM defines the contract for distilling raw claims into atomic facts.
type LLM interface {
	Distill(req DistillRequest) (DistillResult, error)
}

// StdioLLM implements LLM over line-delimited JSON stdio streams.
// It streams DistillRequest to Out and reads DistillResult from In.
type StdioLLM struct {
	In      io.Reader
	Out     io.Writer
	scanner *bufio.Scanner
	mu      sync.Mutex
}

// NewStdioLLM creates an LLM instance backed by input/output streams.
func NewStdioLLM(in io.Reader, out io.Writer) *StdioLLM {
	scanner := bufio.NewScanner(in)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	return &StdioLLM{
		In:      in,
		Out:     out,
		scanner: scanner,
	}
}

// Distill sends one line-delimited request and awaits one line-delimited response.
func (s *StdioLLM) Distill(req DistillRequest) (DistillResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return DistillResult{ClaimID: req.ClaimID, Status: "failed"}, err
	}

	if _, err := fmt.Fprintf(s.Out, "%s\n", string(reqBytes)); err != nil {
		return DistillResult{ClaimID: req.ClaimID, Status: "failed"}, fmt.Errorf("write stdio llm request: %w", err)
	}

	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return DistillResult{ClaimID: req.ClaimID, Status: "failed"}, fmt.Errorf("read stdio llm response: %w", err)
		}
		return DistillResult{ClaimID: req.ClaimID, Status: "failed"}, io.EOF
	}

	line := s.scanner.Text()
	res, err := ParseDistillResponse(line)
	if err != nil {
		return DistillResult{ClaimID: req.ClaimID, Status: "failed"}, err
	}
	res.ClaimID = req.ClaimID
	return res, nil
}

// MockLLM provides deterministic canned responses for testing.
type MockLLM struct {
	Handlers map[string]DistillResult
	Default  DistillResult
}

// Distill returns the canned result for the claim or the default result.
func (m *MockLLM) Distill(req DistillRequest) (DistillResult, error) {
	if res, ok := m.Handlers[req.ClaimID]; ok {
		res.ClaimID = req.ClaimID
		return res, nil
	}
	res := m.Default
	res.ClaimID = req.ClaimID
	return res, nil
}
