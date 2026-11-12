package importmem

import (
	"os"
	"strings"
	"testing"
)

// buildCorpus returns at least 60 secret shapes: the file-backed layers plus
// runtime-generated JWT, PEM, and entropy samples.
func buildCorpus(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/redact/corpus.txt")
	if err != nil {
		t.Fatal(err)
	}
	var shapes []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			shapes = append(shapes, line)
		}
	}
	for i := 0; i < 12; i++ {
		shapes = append(shapes, "value "+syntheticToken()+" end")
	}
	for i := 0; i < 6; i++ {
		shapes = append(shapes, syntheticJWT())
	}
	for i := 0; i < 6; i++ {
		shapes = append(shapes, "-----BEGIN RSA "+"PRIVATE KEY-----\n"+syntheticToken()+"\n-----END RSA "+"PRIVATE KEY-----")
	}
	return shapes
}

func TestRedactionCorpus(t *testing.T) {
	shapes := buildCorpus(t)
	if len(shapes) < 60 {
		t.Fatalf("corpus has %d shapes", len(shapes))
	}
	redactor, err := NewRedactor(RedactorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, shape := range shapes {
		result, err := redactor.Redact(shape, "")
		if err != nil {
			t.Fatalf("shape %d: %v", i, err)
		}
		if result.Counts["secret"] == 0 {
			t.Fatalf("shape %d produced no secret count: %q", i, shape)
		}
		if strings.Contains(result.Text, "pw01") && i < 12 {
			t.Fatalf("shape %d leaked a password: %q", i, result.Text)
		}
	}
}

func BenchmarkRedact100KB(b *testing.B) {
	redactor, err := NewRedactor(RedactorOptions{})
	if err != nil {
		b.Fatal(err)
	}
	data, err := os.ReadFile("testdata/redact/corpus.txt")
	if err != nil {
		b.Fatal(err)
	}
	var builder strings.Builder
	for builder.Len() < 100*1024 {
		builder.Write(data)
	}
	corpus := builder.String()
	b.SetBytes(int64(len(corpus)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := redactor.Redact(corpus, ""); err != nil {
			b.Fatal(err)
		}
	}
}
