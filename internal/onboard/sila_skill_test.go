package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSilaSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "config", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()

	written, err := WriteSilaSkill(repo)
	if err != nil {
		t.Fatal(err)
	}
	repoPath := filepath.Join(repo, ".agents", "skills", "sila", "SKILL.md")
	globalPath := filepath.Join(home, ".agents", "skills", "sila", "SKILL.md")
	for _, path := range []string{repoPath, globalPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != SilaSkillMarkdown {
			t.Fatalf("%s does not match the embedded skill", path)
		}
	}
	for _, parent := range []string{
		filepath.Join(home, ".gemini", "config", "skills"),
		filepath.Join(home, ".claude", "skills"),
	} {
		link := filepath.Join(parent, "sila")
		fi, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("lstat %s: %v", link, err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is not a symlink", link)
		}
		target, err := os.Readlink(link)
		if err != nil {
			t.Fatal(err)
		}
		if target != filepath.Join(home, ".agents", "skills", "sila") {
			t.Fatalf("%s points to %s", link, target)
		}
	}
	if len(written) < 4 {
		t.Fatalf("expected repo/global/symlink writes, got %v", written)
	}

	// Idempotent.
	if _, err := WriteSilaSkill(repo); err != nil {
		t.Fatalf("second write: %v", err)
	}
}

func TestWriteSilaSkillPreservesForeignSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	foreign := filepath.Join(home, ".claude", "skills", "sila")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "SKILL.md"), []byte("foreign skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSilaSkill(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(foreign, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "foreign skill" {
		t.Fatalf("foreign skill was overwritten: %s", data)
	}
}

func TestSilaSkillEmbeddedMatchesRepo(t *testing.T) {
	repoFile := filepath.Join("..", "..", ".agents", "skills", "sila", "SKILL.md")
	data, err := os.ReadFile(repoFile)
	if err != nil {
		t.Fatalf("read canonical skill: %v", err)
	}
	if string(data) != SilaSkillMarkdown {
		t.Fatal("embedded SilaSkillMarkdown differs from .agents/skills/sila/SKILL.md")
	}
	for _, marker := range []string{"sila standup", "sila case", "sila persona", "sila edit-check", "sila facts verify", "sila sync"} {
		if !strings.Contains(SilaSkillMarkdown, marker) {
			t.Fatalf("skill missing marker %q", marker)
		}
	}
}

func TestCheckReportsSilaSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()

	findItem := func(rep *CheckReport) CheckItem {
		for _, item := range rep.Items {
			if item.Name == "Sila skill" {
				return item
			}
		}
		t.Fatal("Sila skill check item missing")
		return CheckItem{}
	}

	rep := Check(nil, repo, "test", false)
	if item := findItem(rep); item.Status != "warn" {
		t.Fatalf("missing skill should warn: %+v", item)
	}
	if _, err := WriteSilaSkill(repo); err != nil {
		t.Fatal(err)
	}
	rep = Check(nil, repo, "test", false)
	if item := findItem(rep); item.Status != "pass" {
		t.Fatalf("provisioned skill should pass: %+v", item)
	}
}

func TestProvisionWritesSilaSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	res := Provision(repo, "test")
	if len(res.SkillsUpdated) == 0 {
		t.Fatalf("Provision did not report skills: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents", "skills", "sila", "SKILL.md")); err != nil {
		t.Fatalf("repo skill missing: %v", err)
	}
}
