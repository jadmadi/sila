package dream

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func readOODQuery(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "ood", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestOODFixtures(t *testing.T) {
	st := oodStore(t)
	inDomain := EvaluateOOD(st, "oodp", readOODQuery(t, "in_domain.txt"), 0.60)
	if inDomain.OutOfDomain {
		t.Fatalf("in-domain fixture rejected: %+v", inDomain)
	}
	nonsense := EvaluateOOD(st, "oodp", readOODQuery(t, "nonsense.txt"), 0.60)
	if !nonsense.OutOfDomain || nonsense.Reason != "no_term_match" {
		t.Fatalf("nonsense fixture accepted: %+v", nonsense)
	}
}

func TestOODGuardP95Under10ms(t *testing.T) {
	if raceEnabled {
		t.Skip("latency budget excludes race instrumentation")
	}
	st := oodStore(t)
	query := readOODQuery(t, "in_domain.txt")
	_ = EvaluateOOD(st, "oodp", query, 0.60)
	runs := 50
	durations := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		_ = EvaluateOOD(st, "oodp", query, 0.60)
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[int(float64(runs)*0.95)-1]
	if p95 > 10*time.Millisecond {
		t.Fatalf("read guard p95 %s exceeds 10ms", p95)
	}
}
