package config

import (
	"strings"
	"testing"
)

func TestTrustTargetsValidation(t *testing.T) {
	sha := strings.Repeat("a", 40)
	cfg, err := loadWelcome(t, `{"trust":{"targets":["`+sha+`","`+sha+`"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Trust.Targets) != 1 || cfg.Trust.Targets[0] != sha {
		t.Fatalf("targets = %v", cfg.Trust.Targets)
	}
	if _, err := loadWelcome(t, `{"trust":{"targets":["deadbeef"]}}`); err == nil || !strings.Contains(err.Error(), "trust.targets") {
		t.Fatalf("err = %v", err)
	}
}
