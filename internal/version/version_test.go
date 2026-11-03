package version

import (
	"strings"
	"testing"
)

func TestVersionString(t *testing.T) {
	s := String()
	if s == "" {
		t.Fatal("expected non-empty version string")
	}
	// When Version is set or inferred
	if !strings.HasPrefix(s, "dev") && !strings.HasPrefix(s, "v") {
		t.Fatalf("unexpected version format: %q", s)
	}
}

func TestIsRelease(t *testing.T) {
	for _, v := range []string{"v0.5.0", "v1.0.0", "v1.2.3-rc.1"} {
		if !IsRelease(v) {
			t.Fatalf("%q should be a release", v)
		}
	}
	for _, v := range []string{"07963d5", "v0.5", "dev+abc", "1.0.0"} {
		if IsRelease(v) {
			t.Fatalf("%q must not be a release", v)
		}
	}
}

// TestReleaseIdentity covers the distinction the release workflow depends on:
// a published tag is a release identity, a Go pseudo-version is a source build,
// and a prerelease is a real release that is not stable.
func TestReleaseIdentity(t *testing.T) {
	cases := []struct {
		v          string
		release    bool
		pseudo     bool
		prerelease bool
	}{
		{"v0.6.0", true, false, false},
		{"v1.2.3", true, false, false},
		{"v0.6.0-rc.1", true, false, true},
		{"v0.6.0-beta.2", true, false, true},
		// go install / module metadata shapes: never a release
		{"v0.0.0-20260101120000-abcdef123456", false, true, false},
		{"v0.5.1-0.20260101120000-abcdef123456", false, true, false},
		{"(devel)", false, false, false},
		{"dev", false, false, false},
		{"0.6.0", false, false, false},
		{"v0.6", false, false, false},
	}
	for _, c := range cases {
		if got := IsRelease(c.v); got != c.release {
			t.Errorf("IsRelease(%q) = %v, want %v", c.v, got, c.release)
		}
		if got := IsPseudoVersion(c.v); got != c.pseudo {
			t.Errorf("IsPseudoVersion(%q) = %v, want %v", c.v, got, c.pseudo)
		}
		if got := IsPrerelease(c.v); got != c.prerelease {
			t.Errorf("IsPrerelease(%q) = %v, want %v", c.v, got, c.prerelease)
		}
	}
}

// TestPrereleaseIsLabeled keeps a prerelease from reading as a stable release.
func TestPrereleaseIsLabeled(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	defer func() { Version, Commit, Date = origV, origC, origD }()

	Version, Commit, Date = "v0.6.0-rc.1", "abc1234", "2026-09-16"
	if got := String(); !strings.Contains(got, "prerelease") {
		t.Fatalf("prerelease not labeled: %q", got)
	}
	Version, Commit, Date = "v0.6.0", "abc1234", "2026-09-16"
	if got := String(); strings.Contains(got, "prerelease") {
		t.Fatalf("stable release labeled as prerelease: %q", got)
	}
}

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		v1   string
		v2   string
		want int
	}{
		{"v0.7.0", "v0.7.0", 0},
		{"0.7.0", "v0.7.0", 0},
		{"v0.8.0", "v0.7.0", 1},
		{"v0.7.0", "v0.8.0", -1},
		{"v1.0.0", "v0.9.9", 1},
		{"v0.9.9", "v1.0.0", -1},
		{"v0.7.1", "v0.7.0", 1},
		{"dev", "v0.7.0", -1},
		{"v0.7.0", "dev", 1},
		{"dev", "dev", 0},
		{"dev+abc-dirty", "dev", 0},
		{"dev+abc-dirty", "v0.7.0", -1},
	}
	for _, c := range cases {
		if got := SemverCompare(c.v1, c.v2); got != c.want {
			t.Errorf("SemverCompare(%q, %q) = %d, want %d", c.v1, c.v2, got, c.want)
		}
	}
}

func TestIsBreakingUpgrade(t *testing.T) {
	cases := []struct {
		cur  string
		lat  string
		want bool
	}{
		{"v0.7.0", "v0.7.1", false},
		{"v0.7.0", "v0.8.0", true},
		{"v1.0.0", "v1.1.0", false},
		{"v1.0.0", "v2.0.0", true},
		{"v0.7.0", "v0.7.0", false},
		{"dev", "v0.8.0", false},
	}
	for _, c := range cases {
		if got := IsBreakingUpgrade(c.cur, c.lat); got != c.want {
			t.Errorf("IsBreakingUpgrade(%q, %q) = %v, want %v", c.cur, c.lat, got, c.want)
		}
	}
}
