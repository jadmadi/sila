package config

import (
	"strings"
	"testing"
)

func TestMemoryMirrorConfig(t *testing.T) {
	cfg, err := loadWelcome(t, `{"memory":{"mirror":"off"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MemoryMirrorEnabled() {
		t.Fatal("off did not disable the mirror")
	}
	cfg, err = loadWelcome(t, `{"memory":{"mirror":"on"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MemoryMirrorEnabled() {
		t.Fatal("on did not enable the mirror")
	}
}

func TestMemoryMirrorEnvOverridesConfig(t *testing.T) {
	t.Setenv("SILA_MEMORY_MIRROR", "on")
	cfg, err := loadWelcome(t, `{"memory":{"mirror":"off"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MemoryMirrorEnabled() {
		t.Fatal("env on did not override off config")
	}
	t.Setenv("SILA_MEMORY_MIRROR", "off")
	cfg, err = loadWelcome(t, `{"memory":{"mirror":"on"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MemoryMirrorEnabled() {
		t.Fatal("env off did not override on config")
	}
}

func TestMemoryMirrorInvalidValue(t *testing.T) {
	_, err := loadWelcome(t, `{"memory":{"mirror":"maybe"}}`)
	if err == nil || !strings.Contains(err.Error(), "memory.mirror") {
		t.Fatalf("error = %v", err)
	}
}
