package loaders

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanJSONL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.jsonl")
	os.WriteFile(p, []byte("{}\n\n{\"a\":1}\n"), 0o644)

	var n int
	if err := ScanJSONL(p, func(line []byte) error {
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 lines, got %d", n)
	}
}

func TestDecodeJSONLStream(t *testing.T) {
	type item struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "stream.jsonl")
	data := "{\"name\":\"first\",\"value\":10}\n{\"name\":\"second\",\"value\":20}\n"
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	var items []item
	if err := ScanJSONLStream[item](p, func(val item) error {
		items = append(items, val)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Name != "first" || items[0].Value != 10 {
		t.Errorf("unexpected item 0: %+v", items[0])
	}
	if items[1].Name != "second" || items[1].Value != 20 {
		t.Errorf("unexpected item 1: %+v", items[1])
	}
}

func BenchmarkDecodeJSONLStream(b *testing.B) {
	type item struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	raw := `{"name":"test_entry","value":42}`
	var buf strings.Builder
	for i := 0; i < 1000; i++ {
		buf.WriteString(raw)
		buf.WriteByte('\n')
	}
	content := buf.String()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := strings.NewReader(content)
		_ = DecodeJSONLStream[item](r, func(val item) error {
			return nil
		})
	}
}
