package version

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

func TestChecksumForAsset(t *testing.T) {
	checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	got, err := checksumForAsset(checksum+"  sila-linux-amd64\n", "sila-linux-amd64")
	if err != nil || got != checksum {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := checksumForAsset(checksum+"  other\n", "sila-linux-amd64"); err == nil {
		t.Fatal("missing asset must fail")
	}
}

func TestVerifyReleaseMetadata(t *testing.T) {
	metadata := releaseMetadata{Version: "v0.5.0", MCPContractVersion: 1, MCPContractSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SchemaVersion: 8, MinimumSchemaVersion: 1}
	if err := validateReleaseMetadata(metadata, "v0.5.0"); err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseMetadata(metadata, "v0.5.1"); err == nil {
		t.Fatal("mismatched tag must fail")
	}
}

func TestUpdateCache(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	now := time.Now().UTC().Truncate(time.Second)
	cache := UpdateCache{
		CheckedAt:     now,
		LatestVersion: "v0.9.5",
	}

	if err := SaveUpdateCache(cache); err != nil {
		t.Fatalf("SaveUpdateCache failed: %v", err)
	}

	loaded, err := LoadUpdateCache()
	if err != nil {
		t.Fatalf("LoadUpdateCache failed: %v", err)
	}

	if loaded.LatestVersion != "v0.9.5" {
		t.Errorf("expected LatestVersion v0.9.5, got %s", loaded.LatestVersion)
	}
	if !loaded.CheckedAt.Equal(now) {
		t.Errorf("expected CheckedAt %v, got %v", now, loaded.CheckedAt)
	}
}

func TestFetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest/version.json" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"version":"v0.9.0","commit":"abcdef1","built_at":"2026-09-29T10:00:00Z","platforms":["linux-amd64"]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	info, err := FetchLatestRelease(server.URL)
	if err != nil {
		t.Fatalf("FetchLatestRelease failed: %v", err)
	}
	if info.Version != "v0.9.0" {
		t.Errorf("expected version v0.9.0, got %s", info.Version)
	}
	if info.Commit != "abcdef1" {
		t.Errorf("expected commit abcdef1, got %s", info.Commit)
	}
}

func TestRunUpgradeCheckOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest/version.json" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"version":"v99.0.0","commit":"abcdef1","built_at":"2026-09-29T10:00:00Z"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	ui := render.New(os.Stdout, true)

	opts := UpdateOptions{
		CheckOnly: true,
		BaseURL:   server.URL,
	}

	if err := RunUpgrade(ui, opts); err != nil {
		t.Fatalf("RunUpgrade failed: %v", err)
	}
}

func TestVerifiedDownloadSuccessAndMismatch(t *testing.T) {
	payload := []byte("#!/bin/sh\necho upgraded\n")
	hash := sha256.Sum256(payload)
	hashHex := fmt.Sprintf("%x", hash)

	targetOS := runtime.GOOS
	targetArch := runtime.GOARCH
	ext := ""
	if targetOS == "windows" {
		ext = ".exe"
	}
	assetName := fmt.Sprintf("sila-%s-%s%s", targetOS, targetArch, ext)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", hashHex, assetName)
		case "/latest/" + assetName:
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	client := &http.Client{Timeout: 5 * time.Second}
	err := verifiedDownload(client, server.URL+"/latest/"+assetName, server.URL+"/latest/checksums.txt", assetName, &out)
	if err != nil {
		t.Fatalf("verifiedDownload failed: %v", err)
	}
	if !bytes.Equal(out.Bytes(), payload) {
		t.Fatalf("payload mismatch")
	}

	// Test mismatch
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/checksums.txt":
			fmt.Fprintf(w, "0000000000000000000000000000000000000000000000000000000000000000  %s\n", assetName)
		case "/latest/" + assetName:
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer badServer.Close()

	var badOut bytes.Buffer
	err = verifiedDownload(client, badServer.URL+"/latest/"+assetName, badServer.URL+"/latest/checksums.txt", assetName, &badOut)
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
}
