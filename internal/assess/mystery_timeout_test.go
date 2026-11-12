package assess

import (
	"strings"
	"testing"
)

func TestWithProbeTestTimeout(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"go test", []string{"go", "test", "-race", "./..."}, "-timeout=40m"},
		{"shell", []string{"sh", "-c", "go test -race ./..."}, "-timeout=40m"},
		{"existing", []string{"go", "test", "-timeout=5m", "./..."}, "-timeout=5m"},
		{"other", []string{"go", "build", "./cmd/sila"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(withProbeTestTimeout(tc.in), " ")
			if tc.want == "" {
				if strings.Contains(got, "-timeout") {
					t.Fatalf("unexpected timeout: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("missing %q in %q", tc.want, got)
			}
		})
	}
}
