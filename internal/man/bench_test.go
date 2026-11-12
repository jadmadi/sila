package man

import "testing"

func BenchmarkLookup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = Lookup("gate")
	}
}

func BenchmarkSearch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Search("handoff")
	}
}
