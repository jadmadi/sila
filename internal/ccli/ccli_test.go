package ccli

import (
	"context"
	"strings"
	"testing"
)

func TestExtractFlags(t *testing.T) {
	catalog := FlagCatalog{
		"--verbose": {Arity: 0, Type: "bool"},
		"--timeout": {Arity: 1, Type: "string"},
		"-v":        {Arity: 0, Type: "bool"},
		"-t":        {Arity: 1, Type: "string"},
	}

	t.Run("EmptyArgs", func(t *testing.T) {
		res := ExtractFlags([]string{}, catalog)
		if res.ExitCode != ExitUsage {
			t.Errorf("expected ExitUsage for empty args, got %d", res.ExitCode)
		}
	})

	t.Run("ValidFlags", func(t *testing.T) {
		args := []string{"--verbose", "deploy", "--timeout=30s", "web", "-v", "-t", "10s"}
		res := ExtractFlags(args, catalog)
		if res.ExitCode != ExitSuccess {
			t.Fatalf("unexpected error: %s", res.ErrorMessage)
		}
		if _, ok := res.Flags["--verbose"]; !ok {
			t.Errorf("missing --verbose flag")
		}
		if res.Flags["--timeout"] != "30s" {
			t.Errorf("expected timeout 30s, got %s", res.Flags["--timeout"])
		}
		if res.Flags["-t"] != "10s" {
			t.Errorf("expected -t 10s, got %s", res.Flags["-t"])
		}
		if len(res.Positional) != 2 || res.Positional[0].Raw != "deploy" || res.Positional[1].Raw != "web" {
			t.Errorf("positional mismatch: %v", res.Positional)
		}
	})

	t.Run("DoubleDash", func(t *testing.T) {
		args := []string{"deploy", "--", "--verbose", "-t", "val"}
		res := ExtractFlags(args, catalog)
		if res.ExitCode != ExitSuccess {
			t.Fatalf("unexpected error: %s", res.ErrorMessage)
		}
		if len(res.Flags) != 0 {
			t.Errorf("expected 0 flags after double dash, got %d", len(res.Flags))
		}
		if len(res.Positional) != 4 {
			t.Errorf("expected 4 positional tokens, got %d", len(res.Positional))
		}
	})

	t.Run("MalformedFlag", func(t *testing.T) {
		args := []string{"deploy", "--=val"}
		res := ExtractFlags(args, catalog)
		if res.ExitCode != ExitUsage {
			t.Errorf("expected ExitUsage on malformed flag, got %d", res.ExitCode)
		}
	})

	t.Run("MissingFlagValue", func(t *testing.T) {
		args := []string{"deploy", "--timeout"}
		res := ExtractFlags(args, catalog)
		if res.ExitCode != ExitUsage {
			t.Errorf("expected ExitUsage on missing value, got %d", res.ExitCode)
		}
	})

	t.Run("MissingShortFlagValue", func(t *testing.T) {
		args := []string{"deploy", "-t"}
		res := ExtractFlags(args, catalog)
		if res.ExitCode != ExitUsage {
			t.Errorf("expected ExitUsage on missing short flag value, got %d", res.ExitCode)
		}
	})

	t.Run("UnknownFlag", func(t *testing.T) {
		args := []string{"deploy", "--unknown"}
		res := ExtractFlags(args, catalog)
		if res.ExitCode != ExitUsage {
			t.Errorf("expected ExitUsage on unknown flag, got %d", res.ExitCode)
		}
	})
}

func TestGuardrails(t *testing.T) {
	t.Run("SecretDetection", func(t *testing.T) {
		ast := &AST{
			ActionID: "user.login",
			Tier:     TierMutation,
			Target:   "admin",
			Operands: map[string]string{"password": "secret_password_123"},
		}
		code, msg, _ := CheckGuardrails(ast, nil, []string{"login", "password", "secret_password_123"})
		if code != ExitPolicy {
			t.Errorf("expected ExitPolicy on secret, got %d (%s)", code, msg)
		}
	})

	t.Run("QueryTargetDefaulting", func(t *testing.T) {
		ast := &AST{
			ActionID: "metrics.show",
			Tier:     TierQuery,
			Operands: make(map[string]string),
		}
		code, _, _ := CheckGuardrails(ast, nil, []string{"show", "metrics"})
		if code != ExitSuccess {
			t.Errorf("expected ExitSuccess, got %d", code)
		}
		if ast.Target != "default" || ast.Operands["target"] != "default" {
			t.Errorf("expected target default, got target=%q", ast.Target)
		}
	})

	t.Run("MutationMissingTarget", func(t *testing.T) {
		ast := &AST{
			ActionID: "service.scale",
			Tier:     TierMutation,
			Operands: make(map[string]string),
		}
		code, msg, _ := CheckGuardrails(ast, nil, []string{"scale", "service"})
		if code != ExitUsage {
			t.Errorf("expected ExitUsage on missing target, got %d (%s)", code, msg)
		}
	})

	t.Run("DestructiveDryRun", func(t *testing.T) {
		var stderr strings.Builder
		streams := NewStreams(strings.NewReader(""), &strings.Builder{}, &stderr, false)
		ast := &AST{
			ActionID: "database.purge",
			Tier:     TierDestructive,
			Target:   "prod_db",
			RawFlags: []string{"--dry-run"},
		}
		code, _, isDryRun := CheckGuardrails(ast, streams, []string{"purge", "database", "prod_db", "--dry-run"})
		if code != ExitSuccess || !isDryRun {
			t.Errorf("expected ExitSuccess and dry run true, got code %d, dryRun %v", code, isDryRun)
		}
	})

	t.Run("DestructiveNonInteractiveNoBypass", func(t *testing.T) {
		streams := NewStreams(strings.NewReader(""), &strings.Builder{}, &strings.Builder{}, false)
		ast := &AST{
			ActionID: "database.purge",
			Tier:     TierDestructive,
			Target:   "prod_db",
		}
		code, _, _ := CheckGuardrails(ast, streams, []string{"purge", "database", "prod_db"})
		if code != ExitPolicy {
			t.Errorf("expected ExitPolicy in non-interactive mode without bypass, got %d", code)
		}
	})

	t.Run("DestructiveInteractiveConfirmation", func(t *testing.T) {
		// User confirms
		streamsYes := NewStreams(strings.NewReader("yes\n"), &strings.Builder{}, &strings.Builder{}, true)
		ast := &AST{
			ActionID: "database.purge",
			Tier:     TierDestructive,
			Target:   "prod_db",
		}
		code, _, _ := CheckGuardrails(ast, streamsYes, []string{"purge", "database", "prod_db"})
		if code != ExitSuccess {
			t.Errorf("expected ExitSuccess on yes confirmation, got %d", code)
		}

		// User declines
		streamsNo := NewStreams(strings.NewReader("no\n"), &strings.Builder{}, &strings.Builder{}, true)
		codeNo, _, _ := CheckGuardrails(ast, streamsNo, []string{"purge", "database", "prod_db"})
		if codeNo != ExitPolicy {
			t.Errorf("expected ExitPolicy on no confirmation, got %d", codeNo)
		}
	})

	t.Run("DestructiveBypassFlags", func(t *testing.T) {
		streams := NewStreams(strings.NewReader(""), &strings.Builder{}, &strings.Builder{}, false)
		ast := &AST{
			ActionID: "database.purge",
			Tier:     TierDestructive,
			Target:   "prod_db",
			RawFlags: []string{"--yes"},
		}
		code, _, _ := CheckGuardrails(ast, streams, []string{"purge", "database", "prod_db", "--yes"})
		if code != ExitSuccess {
			t.Errorf("expected ExitSuccess with --yes bypass, got %d", code)
		}
	})
}

func TestStreams(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder
	stdin := strings.NewReader("yes\n")
	streams := NewStreams(stdin, &stdout, &stderr, true)

	t.Run("EmitDataString", func(t *testing.T) {
		err := streams.EmitData("hello world", false)
		if err != nil {
			t.Fatalf("EmitData failed: %v", err)
		}
		if !strings.Contains(stdout.String(), "hello world") {
			t.Errorf("stdout missing expected data: %s", stdout.String())
		}
	})

	t.Run("EmitDataJSON", func(t *testing.T) {
		stdout.Reset()
		payload := map[string]string{"key": "value"}
		err := streams.EmitData(payload, true)
		if err != nil {
			t.Fatalf("EmitData JSON failed: %v", err)
		}
		if !strings.Contains(stdout.String(), `"key": "value"`) {
			t.Errorf("stdout missing expected JSON: %s", stdout.String())
		}
	})

	t.Run("LoggingToStderr", func(t *testing.T) {
		streams.LogInfo("informational %s", "detail")
		streams.LogError("error %s", "occurred")
		out := stderr.String()
		if !strings.Contains(out, "[INFO] informational detail") || !strings.Contains(out, "[ERROR] error occurred") {
			t.Errorf("stderr logging missing messages: %s", out)
		}
	})

	t.Run("PromptConfirm", func(t *testing.T) {
		confirmed, err := streams.PromptConfirm("Proceed? ")
		if err != nil {
			t.Fatalf("PromptConfirm error: %v", err)
		}
		if !confirmed {
			t.Errorf("expected confirmation true, got false")
		}
	})

	t.Run("DefaultStreams", func(t *testing.T) {
		def := DefaultStreams()
		if def.Stdout == nil || def.Stderr == nil || def.Stdin == nil {
			t.Errorf("DefaultStreams missing handles")
		}
	})
}

func TestCollisionDetector(t *testing.T) {
	t.Run("NilOrSmallInventory", func(t *testing.T) {
		reports, err := DetectCollisions(nil)
		if err != nil || len(reports) != 0 {
			t.Errorf("expected nil reports for nil inventory")
		}

		inv := &Inventory{Productions: []*Production{{ID: "single"}}}
		reports, err = DetectCollisions(inv)
		if err != nil || len(reports) != 0 {
			t.Errorf("expected nil reports for single production inventory")
		}
	})
}

func TestRouterExecution(t *testing.T) {
	inv := NewStandardInventory()
	router := NewRouter(inv)

	router.RegisterAction(&Action{
		ID:   "cluster.status",
		Tier: TierQuery,
		Handler: func(ctx context.Context, ast *AST, streams *Streams) ExitCode {
			streams.EmitData("Cluster is healthy", false)
			return ExitSuccess
		},
	})

	var stdout strings.Builder
	var stderr strings.Builder
	streams := NewStreams(strings.NewReader(""), &stdout, &stderr, false)

	code, res := router.Route(context.Background(), []string{"show", "status", "of", "cluster", "prod-1"}, streams)
	if code != ExitSuccess {
		t.Fatalf("Route failed with exit code %d: %s", code, res.ErrorMessage)
	}
	if !strings.Contains(stdout.String(), "Cluster is healthy") {
		t.Errorf("expected handler output in stdout, got: %s", stdout.String())
	}
}
