package main

import (
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestRenderLauncherListsFourOptions(t *testing.T) {
	menu := renderLauncher(render.New(nil, true))
	for _, want := range []string{
		"quick launcher",
		"Launch Web Cockpit",
		"View Roadmap Matrix",
		"Search Transcripts",
		"Run System Doctor",
		"q to quit",
	} {
		if !strings.Contains(menu, want) {
			t.Errorf("launcher menu missing %q", want)
		}
	}
}

func TestLauncherArgsMapping(t *testing.T) {
	cases := []struct {
		key   byte
		query string
		want  []string
		ok    bool
	}{
		{'1', "", []string{"ui"}, true},
		{'2', "", []string{"goals"}, true},
		{'3', "payment webhook", []string{"search", "payment", "webhook"}, true},
		{'3', "  auth  ", []string{"search", "auth"}, true},
		{'3', "   ", nil, false},
		{'4', "", []string{"doctor"}, true},
		{'q', "", nil, false},
		{'9', "", nil, false},
		{0, "", nil, false},
	}
	for _, tc := range cases {
		got, ok := launcherArgs(tc.key, tc.query)
		if ok != (tc.want != nil) {
			t.Errorf("key %q query %q: ok = %v, want %v", tc.key, tc.query, ok, tc.want != nil)
		}
		if tc.want == nil {
			if ok {
				t.Errorf("key %q: expected abort, got %v", tc.key, got)
			}
			continue
		}
		if !ok {
			t.Errorf("key %q: expected dispatch, got abort", tc.key)
			continue
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("key %q query %q: argv = %v, want %v", tc.key, tc.query, got, tc.want)
		}
	}
}

func TestIsQuitKey(t *testing.T) {
	for _, k := range []byte{'q', 'Q', 3, 27} {
		if !isQuitKey(k) {
			t.Errorf("key %d should quit", k)
		}
	}
	for _, k := range []byte{'1', '2', '3', '4', 'x'} {
		if isQuitKey(k) {
			t.Errorf("key %d should not quit", k)
		}
	}
}

func TestLauncherOptionsShape(t *testing.T) {
	if len(launcherOptions) != 4 {
		t.Fatalf("launcher options = %d, want 4", len(launcherOptions))
	}
	seen := map[string]bool{}
	for _, opt := range launcherOptions {
		if opt.Key == "" || opt.Label == "" || opt.Desc == "" {
			t.Errorf("option %q missing label/description", opt.Key)
		}
		if seen[opt.Key] {
			t.Errorf("duplicate launcher key %q", opt.Key)
		}
		seen[opt.Key] = true
		if opt.Args != nil && len(opt.Args) == 0 {
			t.Errorf("option %q has empty non-nil args", opt.Key)
		}
	}
}
