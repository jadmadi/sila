package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadWelcome(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestWelcomeBackHoursAccepted(t *testing.T) {
	cases := []struct {
		body string
		want time.Duration
	}{
		{`{"state_block":{"welcome_back_hours":24}}`, 24 * time.Hour},
		{`{"state_block":{"welcome_back_hours":0}}`, 0},
		{`{"state_block":{"welcome_back_hours":720}}`, 720 * time.Hour},
		{`{}`, 6 * time.Hour},
	}
	for _, tc := range cases {
		cfg, err := loadWelcome(t, tc.body)
		if err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if got := cfg.WelcomeBackWindow(); got != tc.want {
			t.Fatalf("%s: window = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestWelcomeBackHoursRejected(t *testing.T) {
	for _, body := range []string{
		`{"state_block":{"welcome_back_hours":-1}}`,
		`{"state_block":{"welcome_back_hours":721}}`,
		`{"state_block":{"welcome_back_hours":"abc"}}`,
	} {
		_, err := loadWelcome(t, body)
		if err == nil {
			t.Fatalf("%s: expected an error", body)
		}
		if !strings.Contains(err.Error(), "welcome_back_hours") {
			t.Fatalf("%s: error lacks the field name: %v", body, err)
		}
	}
}
