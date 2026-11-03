package loaders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DrainTarget is the sink interface that receives batches of normalized hook messages.
type DrainTarget interface {
	RecordHookBatch(tool string, msgs []Message) (newRecords, merged int, err error)
}

// DrainTargetFunc allows a plain function to act as a DrainTarget.
type DrainTargetFunc func(tool string, msgs []Message) (newRecords, merged int, err error)

func (f DrainTargetFunc) RecordHookBatch(tool string, msgs []Message) (int, int, error) {
	return f(tool, msgs)
}

// SpoolEntry represents a queued hook envelope on disk with its source path.
type SpoolEntry struct {
	Path     string
	Envelope *HookEnvelope
}

// DrainReport summarizes the outcome of draining the local hook spool into the store.
type DrainReport struct {
	TotalEntries int      `json:"total_entries"`
	Drained      int      `json:"drained"`
	NewRecords   int      `json:"new_records"`
	Merged       int      `json:"merged"`
	Errors       []string `json:"errors,omitempty"`
}

// Spooler manages a high-throughput, non-blocking local FIFO event queue on disk.
type Spooler struct {
	spoolDir string
	mu       sync.RWMutex
}

// NewSpooler initializes a Spooler at the given directory (or DefaultSpoolDir if empty).
func NewSpooler(dir string) (*Spooler, error) {
	if dir == "" {
		dir = DefaultSpoolDir()
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create spool dir %s: %w", dir, err)
	}
	return &Spooler{spoolDir: dir}, nil
}

// SpoolDir returns the underlying queue directory on disk.
func (s *Spooler) SpoolDir() string {
	return s.spoolDir
}

// Spool writes a HookEnvelope atomically to the spool queue.
// Guarantees sub-5ms completion without blocking on database locks.
func (s *Spooler) Spool(env *HookEnvelope) (string, error) {
	if env == nil {
		return "", fmt.Errorf("nil hook envelope")
	}

	data, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("marshal envelope: %w", err)
	}

	if len(data) > MaxHookPayloadBytes {
		return "", ErrPayloadTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	agentClean := sanitizeFilename(env.Agent)
	if agentClean == "" {
		agentClean = "agent"
	}
	finalName := fmt.Sprintf("%020d-%s-%s.json", now.UnixNano(), agentClean, generateRandomID("spool"))
	finalPath := filepath.Join(s.spoolDir, finalName)
	tmpPath := filepath.Join(s.spoolDir, "."+finalName+".tmp")

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return "", fmt.Errorf("write spool tmp file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("atomic rename spool file: %w", err)
	}

	return finalPath, nil
}

// PendingCount returns the number of pending event records in the spool queue.
func (s *Spooler) PendingCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.spoolDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			count++
		}
	}
	return count, nil
}

// ReadPending retrieves all pending spool entries in chronological FIFO order.
func (s *Spooler) ReadPending() ([]*SpoolEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.spoolDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var jsonNames []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, ".") {
			jsonNames = append(jsonNames, name)
		}
	}

	sort.Strings(jsonNames)

	var result []*SpoolEntry
	for _, name := range jsonNames {
		p := filepath.Join(s.spoolDir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		env, err := ParseHookEnvelope(data)
		if err != nil {
			continue
		}
		result = append(result, &SpoolEntry{
			Path:     p,
			Envelope: env,
		})
	}

	return result, nil
}

// Drain processes spooled events, scrubs secrets, writes clean observations into the target sink,
// and deletes processed spool files upon successful persistence.
func (s *Spooler) Drain(target DrainTarget, project *string) (*DrainReport, error) {
	if target == nil {
		return nil, fmt.Errorf("nil drain target provided")
	}

	entries, err := s.ReadPending()
	if err != nil {
		return nil, fmt.Errorf("read pending spool entries: %w", err)
	}

	report := &DrainReport{
		TotalEntries: len(entries),
		Errors:       []string{},
	}

	if len(entries) == 0 {
		return report, nil
	}

	// Group by tool/agent for batched insertion
	type batchItem struct {
		spoolPath string
		msg       Message
	}

	batches := make(map[string][]batchItem)

	for _, entry := range entries {
		env := entry.Envelope
		if project != nil && *project != "" && env.ProjectID != *project {
			continue
		}

		cleanEnv := ScrubEnvelope(env)
		tool := "hook:" + cleanEnv.Agent
		if cleanEnv.Agent == "" {
			tool = "hook:agent"
		}

		// Build normalized content payload
		payloadJSON, err := json.Marshal(cleanEnv.Payload)
		if err != nil {
			payloadJSON = []byte("{}")
		}

		parsedTS, err := time.Parse(time.RFC3339Nano, cleanEnv.Timestamp)
		if err != nil {
			parsedTS = time.Now().UTC()
		}

		contentSummary := fmt.Sprintf("[%s] %s: %s", cleanEnv.Event, cleanEnv.Agent, string(payloadJSON))

		msg := Message{
			Tool:           tool,
			SessionID:      cleanEnv.SessionID,
			ProjectDir:     cleanEnv.ProjectID,
			TS:             parsedTS,
			Role:           "hook",
			Content:        contentSummary,
			SourceLocation: "hook:" + string(cleanEnv.Event),
			SourceOrder:    0,
		}

		batches[tool] = append(batches[tool], batchItem{
			spoolPath: entry.Path,
			msg:       msg,
		})
	}

	for tool, items := range batches {
		msgs := make([]Message, len(items))
		for i, it := range items {
			msgs[i] = it.msg
		}

		newRecs, merged, err := target.RecordHookBatch(tool, msgs)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("commit tool %s: %v", tool, err))
			continue
		}

		report.NewRecords += newRecs
		report.Merged += merged
		report.Drained += len(items)

		// Delete spooled files after successful persistence
		for _, it := range items {
			_ = os.Remove(it.spoolPath)
		}
	}

	return report, nil
}

// DrainWorker runs a background polling loop draining spooled events periodically.
type DrainWorker struct {
	spooler  *Spooler
	target   DrainTarget
	interval time.Duration
	trigger  chan struct{}
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewDrainWorker initializes a background drain worker for a spooler and sink target.
func NewDrainWorker(spooler *Spooler, target DrainTarget, interval time.Duration) *DrainWorker {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &DrainWorker{
		spooler:  spooler,
		target:   target,
		interval: interval,
		trigger:  make(chan struct{}, 1),
		stop:     make(chan struct{}),
	}
}

// Start begins the asynchronous drain worker loop.
func (w *DrainWorker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stop:
				return
			case <-w.trigger:
				_, _ = w.spooler.Drain(w.target, nil)
			case <-ticker.C:
				_, _ = w.spooler.Drain(w.target, nil)
			}
		}
	}()
}

// Trigger prompts the worker to execute a drain pass immediately.
func (w *DrainWorker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

// Stop terminates the background worker gracefully.
func (w *DrainWorker) Stop() {
	close(w.stop)
	w.wg.Wait()
}

func sanitizeFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
