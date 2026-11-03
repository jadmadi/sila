package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestPrimeJSONOnFreshStoreReportsInitialization(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "fresh.db")
	ui := render.New(os.Stdout, true)

	out := captureStdout(t, func() {
		runPrime(ui, dbPath, "", true, false, 5, "")
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("prime --json is not valid JSON: %v\n%s", err, out)
	}
	if payload["initialized"] != false {
		t.Fatalf("initialized = %#v, want false on first contact", payload["initialized"])
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("prime must initialize the store: %v", err)
	}
}

func TestOpenReadStoreInitializesMissingStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fresh.db")

	st, created, err := openReadStore(dbPath)
	if err != nil {
		t.Fatalf("openReadStore: %v", err)
	}
	if !created {
		t.Fatal("a missing store must be reported as created")
	}
	if st == nil {
		t.Fatal("expected a usable store")
	}
	if !st.ReadOnly {
		t.Fatal("state-reporting callers must receive a read-only handle")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dbPath); err != nil {
		t.Fatalf("store file was not created: %v", err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Fatalf("store permissions = %o, want 600", fi.Mode().Perm())
	}

	st2, createdAgain, err := openReadStore(dbPath)
	if err != nil {
		t.Fatalf("second openReadStore: %v", err)
	}
	if createdAgain {
		t.Fatal("an existing store must not be reported as created")
	}
	_ = st2.Close()
}

func TestOpenReadStoreMigratesUninitializedSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "zero.db")
	if err := os.WriteFile(dbPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	st, created, err := openReadStore(dbPath)
	if err != nil {
		t.Fatalf("an empty existing store must be migratable: %v", err)
	}
	if created {
		t.Fatal("an existing file must not be reported as created")
	}
	if !st.ReadOnly {
		t.Fatal("expected a read-only handle after migration")
	}
	_ = st.Close()
}

func TestOpenReadStoreConcurrentFirstRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _, err := openReadStore(dbPath)
			if err != nil {
				errs <- err
				return
			}
			_ = st.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent first run failed: %v", err)
	}
	if st, _, err := openReadStore(dbPath); err != nil {
		t.Fatalf("store unusable after concurrent first run: %v", err)
	} else {
		_ = st.Close()
	}
}

func TestFirstContactCommandMatrix(t *testing.T) {
	if os.Getenv("SILA_FIRST_CONTACT_HELPER") == "1" {
		args := strings.Split(os.Getenv("SILA_FIRST_CONTACT_ARGS"), "\x1f")
		os.Args = append([]string{"sila"}, args...)
		main()
		return
	}

	cases := [][]string{
		{"prime"},
		{"prime", "--json"},
		{"recall", "sqlite"},
		{"search", "sqlite"},
		{"doctor"},
		{"digest"},
		{"recap"},
		{"standup"},
		{"goals"},
		{"lesson", "list"},
	}
	for _, args := range cases {
		name := strings.Join(args, "_")
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			repo := filepath.Join(root, "repo")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=TestFirstContactCommandMatrix")
			cmd.Dir = repo
			cmd.Env = append(cleanSilaEnv(),
				"SILA_FIRST_CONTACT_HELPER=1",
				"SILA_FIRST_CONTACT_ARGS="+strings.Join(args, "\x1f"),
				"HOME="+home,
				"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
				"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
				"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
				"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("sila %s exited non-zero on a fresh HOME: %v\n%s", strings.Join(args, " "), err, out)
			}
		})
	}
}

func cleanSilaEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "SILA_") {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func TestOpenReadStoreRejectsUnwritableParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	if _, _, err := openReadStore(filepath.Join(locked, "sila.db")); err == nil {
		t.Fatal("expected an error for an unwritable store directory")
	}
}
