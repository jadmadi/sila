package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrHLCInvalidFormat is returned when an HLC string does not conform to RFC 004 format.
	ErrHLCInvalidFormat = errors.New("hlc: invalid format, expected <13-digits>-<6-hex>-<node_id>")
	// ErrHLCDriftExceeded is returned when an observed remote HLC has physical time exceeding max allowed drift.
	ErrHLCDriftExceeded = errors.New("hlc: remote clock drift exceeded maximum allowable threshold")
)

var (
	validNodeIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	defaultHLC       *HybridLogicalClock
	defaultHLCOnce   sync.Once
)

// DefaultMaxDriftMs is the maximum allowable future drift for an observed remote HLC (60 seconds).
const DefaultMaxDriftMs = int64(60000)

// HybridLogicalClock implements RFC 004-style fixed-width lexicographic Hybrid Logical Clocks
// guaranteeing deterministic causal ordering across distributed agent sandboxes and edge replicas.
type HybridLogicalClock struct {
	mu         sync.Mutex
	latestMs   int64
	counter    uint32
	nodeID     string
	maxDriftMs int64
}

// NewHLC creates a thread-safe HybridLogicalClock with the specified nodeID.
// If nodeID is empty, a deterministic hostname/PID-based or random identifier is generated.
func NewHLC(nodeID string) *HybridLogicalClock {
	cleanNode := sanitizeNodeID(nodeID)
	if cleanNode == "" {
		cleanNode = defaultNodeID()
	}
	return &HybridLogicalClock{
		latestMs:   0,
		counter:    0,
		nodeID:     cleanNode,
		maxDriftMs: DefaultMaxDriftMs,
	}
}

// SetMaxDriftMs configures the maximum permitted future drift in milliseconds.
func (c *HybridLogicalClock) SetMaxDriftMs(driftMs int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if driftMs <= 0 {
		driftMs = DefaultMaxDriftMs
	}
	c.maxDriftMs = driftMs
}

// NodeID returns the clock's node identifier.
func (c *HybridLogicalClock) NodeID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodeID
}

// Tick generates the next strictly monotonic HLC string for a local operation.
// Format: <unix_ms:13 decimal digits>-<counter:6 hex digits>-<node_id>
// Example: "001783038849123-000001-agent_claude_a1b2"
func (c *HybridLogicalClock) Tick(now time.Time) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	physicalMs := now.UnixMilli()

	if physicalMs > c.latestMs {
		c.latestMs = physicalMs
		c.counter = 0
	} else {
		// Clock regressed or same millisecond: advance counter monotonically
		c.counter++
	}

	return fmt.Sprintf("%013d-%06x-%s", c.latestMs, c.counter, c.nodeID)
}

// Observe absorbs a remote HLC string from another agent or edge replica.
// All subsequent local ticks are guaranteed to causally follow the observed timestamp.
func (c *HybridLogicalClock) Observe(remoteHLC string, now time.Time) error {
	remoteMs, remoteCounter, _, err := ParseHLC(remoteHLC)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	physicalMs := now.UnixMilli()

	// Guard against unbounded future drift
	if remoteMs-physicalMs > c.maxDriftMs {
		return fmt.Errorf("%w: remote %d ms is %d ms ahead of physical %d ms (limit: %d ms)",
			ErrHLCDriftExceeded, remoteMs, remoteMs-physicalMs, physicalMs, c.maxDriftMs)
	}

	maxMs := physicalMs
	if c.latestMs > maxMs {
		maxMs = c.latestMs
	}
	if remoteMs > maxMs {
		maxMs = remoteMs
	}

	if maxMs == c.latestMs && maxMs == remoteMs {
		if remoteCounter > c.counter {
			c.counter = remoteCounter + 1
		} else {
			c.counter++
		}
	} else if maxMs == c.latestMs {
		c.counter++
	} else if maxMs == remoteMs {
		c.counter = remoteCounter + 1
	} else {
		// Physical time strictly advanced past both local and remote
		c.counter = 0
	}

	c.latestMs = maxMs
	return nil
}

// State returns the current internal physical time and logical counter.
func (c *HybridLogicalClock) State() (latestMs int64, counter uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latestMs, c.counter
}

// ParseHLC extracts physical milliseconds, logical counter, and nodeID from an HLC string.
func ParseHLC(hlc string) (ms int64, counter uint32, nodeID string, err error) {
	parts := strings.SplitN(hlc, "-", 3)
	if len(parts) != 3 {
		return 0, 0, "", fmt.Errorf("%w: invalid segment count", ErrHLCInvalidFormat)
	}

	if len(parts[0]) < 13 || len(parts[0]) > 16 {
		return 0, 0, "", fmt.Errorf("%w: timestamp must be between 13 and 16 digits", ErrHLCInvalidFormat)
	}
	msVal, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || msVal < 0 {
		return 0, 0, "", fmt.Errorf("%w: invalid timestamp digits", ErrHLCInvalidFormat)
	}

	if len(parts[1]) != 6 {
		return 0, 0, "", fmt.Errorf("%w: counter must be exactly 6 hex characters", ErrHLCInvalidFormat)
	}
	counterVal, err := strconv.ParseUint(parts[1], 16, 32)
	if err != nil {
		return 0, 0, "", fmt.Errorf("%w: invalid counter hex", ErrHLCInvalidFormat)
	}

	nodeVal := parts[2]
	if nodeVal == "" || !validNodeIDRegex.MatchString(nodeVal) {
		return 0, 0, "", fmt.Errorf("%w: invalid node identifier", ErrHLCInvalidFormat)
	}

	return msVal, uint32(counterVal), nodeVal, nil
}

// CompareHLC performs lexicographic comparison between two HLC strings.
// Returns -1 if a < b, 1 if a > b, and 0 if a == b.
// Because HLCs are fixed-width and zero-padded, string comparison is identical to causal ordering.
func CompareHLC(a, b string) int {
	return strings.Compare(a, b)
}

// DefaultHLC returns the process-wide default HybridLogicalClock.
func DefaultHLC() *HybridLogicalClock {
	defaultHLCOnce.Do(func() {
		defaultHLC = NewHLC("")
	})
	return defaultHLC
}

// GenerateHLC generates a new strictly monotonic HLC string from the default process clock.
func GenerateHLC() string {
	return DefaultHLC().Tick(time.Now())
}

// ObserveHLC absorbs a remote HLC timestamp into the default process clock.
func ObserveHLC(remoteHLC string) error {
	return DefaultHLC().Observe(remoteHLC, time.Now())
}

func sanitizeNodeID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func defaultNodeID() string {
	hostname, _ := os.Hostname()
	pid := os.Getpid()
	if hostname == "" {
		hostname = "node"
	} else {
		// Clean hostname
		parts := strings.Split(hostname, ".")
		hostname = parts[0]
	}
	hostname = sanitizeNodeID(hostname)
	if len(hostname) > 12 {
		hostname = hostname[:12]
	}

	// 4 random bytes for collision-free uniqueness across sandboxes
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%d_%s", hostname, pid, hex.EncodeToString(b[:]))
}
