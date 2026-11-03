package git

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Trailer represents a key-value token in a Git commit trailer block (RFC 822 / Git commit trailer).
type Trailer struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// trailerLineRegex matches standard Git trailer lines of the form "Key: Value".
var trailerLineRegex = regexp.MustCompile(`^[A-Za-z0-9-]+:\s*(.*)$`)

// ParseCommitTrailers extracts Git commit trailers from the end of a commit message.
// Following git-interpret-trailers conventions, trailers reside in the final paragraph
// of the commit message (ignoring commented lines starting with '#').
func ParseCommitTrailers(message string) []Trailer {
	if strings.TrimSpace(message) == "" {
		return nil
	}

	lines := strings.Split(message, "\n")
	// Clean comments and trailing empty lines
	var clean []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		clean = append(clean, l)
	}

	// Remove trailing whitespace lines
	for len(clean) > 0 && strings.TrimSpace(clean[len(clean)-1]) == "" {
		clean = clean[:len(clean)-1]
	}
	if len(clean) == 0 {
		return nil
	}

	// Find the trailer block (contiguous lines at the end of the message)
	var trailerBlock []string
	for i := len(clean) - 1; i >= 0; i-- {
		line := strings.TrimSpace(clean[i])
		if line == "" {
			break
		}
		trailerBlock = append([]string{clean[i]}, trailerBlock...)
	}

	var trailers []Trailer
	for _, line := range trailerBlock {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Ensure key contains valid git trailer characters
		if isValidTrailerKey(key) {
			trailers = append(trailers, Trailer{
				Key:   key,
				Value: val,
			})
		}
	}
	return trailers
}

func isValidTrailerKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// GetCommitTrailer returns the value of the first matching trailer key (case-insensitive).
func GetCommitTrailer(message, key string) (string, bool) {
	trailers := ParseCommitTrailers(message)
	lowerKey := strings.ToLower(key)
	for _, t := range trailers {
		if strings.ToLower(t.Key) == lowerKey {
			return t.Value, true
		}
	}
	return "", false
}

// HasSkipArchTrailer checks if the commit message contains the Sila-Skip-Arch trailer.
func HasSkipArchTrailer(message string) (string, bool) {
	return GetCommitTrailer(message, "Sila-Skip-Arch")
}

// ReadCommitEditMsg reads the pending commit message from .git/COMMIT_EDITMSG if present.
func ReadCommitEditMsg(repoRoot string) string {
	path := filepath.Join(repoRoot, ".git", "COMMIT_EDITMSG")
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// ReadLatestCommitMessage reads the HEAD commit message using git log or direct read.
func ReadLatestCommitMessage(repoRoot string) string {
	msgFile := filepath.Join(repoRoot, ".git", "COMMIT_EDITMSG")
	if b, err := os.ReadFile(msgFile); err == nil && len(b) > 0 {
		return string(b)
	}
	// Fallback to git log -1 --pretty=%B
	return ""
}

// ScanCommitMessageFile reads and parses trailers from a specific commit message file (e.g. from hook args).
func ScanCommitMessageFile(path string) ([]Trailer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
		sb.WriteString("\n")
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ParseCommitTrailers(sb.String()), nil
}
