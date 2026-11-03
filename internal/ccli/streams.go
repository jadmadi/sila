package ccli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mattn/go-isatty"
)

// ErrInterrupted is returned when a confirmation prompt is aborted by Ctrl-C (SIGINT).
var ErrInterrupted = errors.New("interrupted by signal")

// Streams provides strict separation between data payload output and diagnostic streams.
type Streams struct {
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	StdinIsTTY    bool
	StderrIsTTY   bool
	IsInteractive bool
	splitTTYSet   bool
}

// IsFileTTY reports whether r is an *os.File connected to a real terminal via isatty.
// Unlike os.ModeCharDevice, /dev/null and pipes return false.
func IsFileTTY(r any) bool {
	if f, ok := r.(*os.File); ok && f != nil {
		return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	}
	return false
}

// NewStreams constructs a Streams instance with explicit streams and interactive flag.
func NewStreams(stdin io.Reader, stdout io.Writer, stderr io.Writer, isInteractive bool) *Streams {
	return &Streams{
		Stdin:         stdin,
		Stdout:        stdout,
		Stderr:        stderr,
		StdinIsTTY:    isInteractive,
		StderrIsTTY:   isInteractive,
		IsInteractive: isInteractive,
		splitTTYSet:   true,
	}
}

// NewStreamsWithTTY constructs a Streams instance with separate stdin/stderr TTY flags.
func NewStreamsWithTTY(stdin io.Reader, stdout io.Writer, stderr io.Writer, stdinIsTTY, stderrIsTTY bool) *Streams {
	return &Streams{
		Stdin:         stdin,
		Stdout:        stdout,
		Stderr:        stderr,
		StdinIsTTY:    stdinIsTTY,
		StderrIsTTY:   stderrIsTTY,
		IsInteractive: stdinIsTTY && stderrIsTTY,
		splitTTYSet:   true,
	}
}

// DefaultStreams returns standard operating system streams with separate isatty detection for stdin and stderr.
func DefaultStreams() *Streams {
	stdinTTY := IsFileTTY(os.Stdin)
	stderrTTY := IsFileTTY(os.Stderr)
	return &Streams{
		Stdin:         os.Stdin,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		StdinIsTTY:    stdinTTY,
		StderrIsTTY:   stderrTTY,
		IsInteractive: stdinTTY && stderrTTY,
		splitTTYSet:   true,
	}
}

// Interactive reports whether both stdin and stderr are interactive terminals.
func (s *Streams) Interactive() bool {
	if s == nil {
		return false
	}
	if s.splitTTYSet || s.StdinIsTTY || s.StderrIsTTY {
		return s.StdinIsTTY && s.StderrIsTTY
	}
	return s.IsInteractive
}

// EmitData writes clean payload data or formatted JSON strictly to stdout.
func (s *Streams) EmitData(data any, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(s.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	if str, ok := data.(string); ok {
		_, err := fmt.Fprintln(s.Stdout, str)
		return err
	}
	_, err := fmt.Fprintf(s.Stdout, "%v\n", data)
	return err
}

// LogInfo writes an informational log entry strictly to stderr.
func (s *Streams) LogInfo(msg string, args ...any) {
	if s != nil && s.Stderr != nil {
		fmt.Fprintf(s.Stderr, "[INFO] "+msg+"\n", args...)
	}
}

// LogError writes an error message strictly to stderr.
func (s *Streams) LogError(msg string, args ...any) {
	if s != nil && s.Stderr != nil {
		fmt.Fprintf(s.Stderr, "[ERROR] "+msg+"\n", args...)
	}
}

// PromptConfirm writes a confirmation prompt to stderr and reads user response from stdin.
// Returns true only if the user explicitly responds with affirmative 'yes' or 'y'.
// Returns ErrInterrupted if Ctrl-C (SIGINT or ASCII ETX 0x03) occurs during the prompt.
func (s *Streams) PromptConfirm(prompt string) (bool, error) {
	if s == nil || s.Stderr == nil || s.Stdin == nil {
		return false, errors.New("streams not available for prompt")
	}
	fmt.Fprint(s.Stderr, prompt)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGINT)
	defer signal.Stop(sigCh)

	type readResult struct {
		line string
		err  error
	}
	resCh := make(chan readResult, 1)
	go func() {
		reader := bufio.NewReader(s.Stdin)
		line, err := reader.ReadString('\n')
		resCh <- readResult{line: line, err: err}
	}()

	select {
	case <-sigCh:
		fmt.Fprintln(s.Stderr)
		return false, ErrInterrupted
	case r := <-resCh:
		if errors.Is(r.err, ErrInterrupted) || strings.ContainsRune(r.line, '\x03') {
			return false, ErrInterrupted
		}
		if r.err != nil && r.err != io.EOF {
			return false, r.err
		}
		trimmed := strings.TrimSpace(strings.ToLower(r.line))
		if trimmed == "y" || trimmed == "yes" {
			return true, nil
		}
		return false, nil
	}
}
