package loaders

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GitLoader reads git commit logs from a repository and converts them into
// normalized messages with role="commit".
type GitLoader struct {
	repoDir string
}

// NewGitLoader creates a GitLoader for the specified directory or current working directory.
func NewGitLoader(repoDir string) Loader {
	if repoDir == "" {
		if wd, err := os.Getwd(); err == nil {
			repoDir = wd
		}
	}
	return &GitLoader{repoDir: repoDir}
}

// Name returns the Git loader identifier.
func (g *GitLoader) Name() string {
	return "git"
}

func findGitRoot(dir string) (string, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", dir)
	}
	return strings.TrimSpace(string(out)), nil
}

// Load extracts git commits since the given watermark (RFC3339 timestamp or empty).
func (g *GitLoader) Load(watermark string, project *string) ([]Message, string, error) {
	targetDir := g.repoDir
	if project != nil && *project != "" {
		targetDir = *project
	}

	gitRoot, err := findGitRoot(targetDir)
	if err != nil {
		return nil, watermark, nil // Not a git repo, return gracefully
	}

	args := []string{
		"-C", gitRoot,
		"log",
		"--name-only",
		"--format=COMMIT_DELIM%n%H%n%an%n%aI%n%s%n%b%nFILES_DELIM",
		"-n", "500",
	}

	if watermark != "" {
		args = append(args, "--since="+watermark)
	}

	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, watermark, nil
	}

	raw := string(out)
	if strings.TrimSpace(raw) == "" {
		return nil, watermark, nil
	}

	chunks := strings.Split(raw, "COMMIT_DELIM\n")
	var msgs []Message
	var latestTS time.Time

	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		parts := strings.Split(chunk, "\nFILES_DELIM\n")
		headerAndBody := parts[0]
		var filesPart string
		if len(parts) > 1 {
			filesPart = strings.TrimSpace(parts[1])
		}

		lines := strings.Split(headerAndBody, "\n")
		if len(lines) < 4 {
			continue
		}

		hash := lines[0]
		author := lines[1]
		tsStr := lines[2]
		subject := lines[3]
		var body string
		if len(lines) > 4 {
			body = strings.TrimSpace(strings.Join(lines[4:], "\n"))
		}

		t, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			continue
		}

		if t.After(latestTS) {
			latestTS = t
		}

		if watermark != "" {
			if wmTime, err := time.Parse(time.RFC3339, watermark); err == nil {
				if !t.After(wmTime) {
					continue
				}
			}
		}

		var content strings.Builder
		hashPrefix := hash
		if len(hashPrefix) > 8 {
			hashPrefix = hashPrefix[:8]
		}
		content.WriteString(fmt.Sprintf("Git Commit %s by %s on %s:\n%s\n", hashPrefix, author, t.Format("2006-01-02 15:04"), subject))
		if body != "" {
			content.WriteString("\n" + body + "\n")
		}
		if filesPart != "" {
			content.WriteString("\nModified Files:\n")
			for f := range strings.SplitSeq(filesPart, "\n") {
				f = strings.TrimSpace(f)
				if f != "" {
					content.WriteString("- " + f + "\n")
				}
			}
		}

		msgs = append(msgs, Message{
			Tool:       "git",
			SessionID:  hash,
			ProjectDir: gitRoot,
			TS:         t.UTC(),
			Role:       "commit",
			Content:    strings.TrimSpace(content.String()),
		})
	}

	newWatermark := watermark
	if !latestTS.IsZero() {
		newWatermark = latestTS.UTC().Format(time.RFC3339)
	}

	return msgs, newWatermark, nil
}

// Count returns the count of commits in the git repository.
func (g *GitLoader) Count(project *string) (int64, error) {
	targetDir := g.repoDir
	if project != nil && *project != "" {
		targetDir = *project
	}
	gitRoot, err := findGitRoot(targetDir)
	if err != nil {
		return 0, nil
	}

	cmd := exec.Command("git", "-C", gitRoot, "rev-list", "--count", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return 0, nil
	}
	var cnt int64
	_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &cnt)
	return cnt, nil
}
