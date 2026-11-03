package gate

import (
	"strings"
	"testing"
	"time"
)

func BenchmarkScanArchDiff_LargeDiff(b *testing.B) {
	// Construct a realistic 5,000-line git diff
	var sb strings.Builder
	sb.WriteString("diff --git a/worker/src/index.ts b/worker/src/index.ts\n")
	sb.WriteString("--- a/worker/src/index.ts\n")
	sb.WriteString("+++ b/worker/src/index.ts\n")
	sb.WriteString("@@ -1,100 +1,2500 @@\n")
	for i := 0; i < 2500; i++ {
		sb.WriteString("+  const val = computeItem(")
		sb.WriteString("i")
		sb.WriteString(");\n")
	}

	sb.WriteString("diff --git a/cmd/sila/main.go b/cmd/sila/main.go\n")
	sb.WriteString("--- a/cmd/sila/main.go\n")
	sb.WriteString("+++ b/cmd/sila/main.go\n")
	sb.WriteString("@@ -1,100 +1,2500 @@\n")
	for i := 0; i < 2500; i++ {
		sb.WriteString("+  if condition {\n+    doSomething()\n+  }\n")
	}

	diff := sb.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := ScanArchDiff(diff)
		if !res.Clean {
			b.Fatal("expected clean diff")
		}
	}
}

func TestArchSentry_LatencyUnder50ms(t *testing.T) {
	// Verify that a 5,000-line diff is scanned in well under 50ms
	var sb strings.Builder
	sb.WriteString("diff --git a/worker/src/index.ts b/worker/src/index.ts\n")
	sb.WriteString("--- a/worker/src/index.ts\n")
	sb.WriteString("+++ b/worker/src/index.ts\n")
	sb.WriteString("@@ -1,100 +1,2500 @@\n")
	for i := 0; i < 2500; i++ {
		sb.WriteString("+  const val = computeItem(i);\n")
	}

	sb.WriteString("diff --git a/cmd/sila/main.go b/cmd/sila/main.go\n")
	sb.WriteString("--- a/cmd/sila/main.go\n")
	sb.WriteString("+++ b/cmd/sila/main.go\n")
	sb.WriteString("@@ -1,100 +1,2500 @@\n")
	for i := 0; i < 2500; i++ {
		sb.WriteString("+  if condition {\n+    doSomething()\n+  }\n")
	}

	diff := sb.String()

	start := time.Now()
	res := ScanArchDiff(diff)
	elapsed := time.Since(start)

	if !res.Clean {
		t.Fatal("expected clean diff")
	}

	if elapsed > 50*time.Millisecond {
		t.Fatalf("scan exceeded 50ms SLA: took %v", elapsed)
	}
	t.Logf("5,000-line diff scanned in %v (<50ms SLA verified)", elapsed)
}
