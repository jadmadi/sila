package ingest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestCrawlLLMsTxtDiscovery(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/llms.txt" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "# Project Documentation\n\nThis is the complete LLMs guide for the project.")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "crawler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	opts := DefaultCrawlOptions()
	opts.AllowPrivate = true
	docs, err := CrawlAndIngest(st, ts.URL, "test-proj", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc from llms.txt, got %d", len(docs))
	}
	if docs[0].Content != "# Project Documentation\n\nThis is the complete LLMs guide for the project.\n" {
		t.Fatalf("unexpected content: %s", docs[0].Content)
	}
}

func TestCrawlSitemapDiscovery(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/docs/intro</loc></url>
  <url><loc>%s/docs/api</loc></url>
</urlset>`, ts.URL, ts.URL)
		case "/docs/intro":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "<html><head><title>Introduction</title></head><body><h1>Introduction</h1><p>Getting started guide.</p></body></html>")
		case "/docs/api":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "<html><head><title>API Reference</title></head><body><h1>API</h1><p>Full API reference.</p></body></html>")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "crawler_sitemap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	opts := DefaultCrawlOptions()
	opts.AllowPrivate = true
	docs, err := CrawlAndIngest(st, ts.URL+"/docs", "test-proj", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs from sitemap, got %d", len(docs))
	}
}

func TestCrawlBFSSite(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/guide":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `<html><head><title>Main Guide</title></head><body><h1>Main Guide</h1><a href="/guide/page1">Page 1</a><a href="/external">External</a></body></html>`)
		case "/guide/page1":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `<html><head><title>Page 1</title></head><body><h1>Page 1 Content</h1><p>Details on page 1.</p></body></html>`)
		case "/external":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `<html><body><h1>Should not be crawled</h1></body></html>`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "crawler_bfs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	opts := DefaultCrawlOptions()
	opts.AllowPrivate = true
	opts.MaxDepth = 1
	docs, err := CrawlAndIngest(st, ts.URL+"/guide", "test-proj", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs from BFS within prefix /guide, got %d", len(docs))
	}
}

func TestCrawlAndIngest_PreCancelledContext(t *testing.T) {
	var requested int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requested, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "content")
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "precancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	opts := DefaultCrawlOptions()
	opts.AllowPrivate = true
	docs, err := CrawlAndIngestContext(ctx, st, ts.URL, "test-proj", opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got err=%v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs, got %d", len(docs))
	}
	if reqCount := atomic.LoadInt32(&requested); reqCount != 0 {
		t.Fatalf("expected 0 requests made, got %d", reqCount)
	}
}

func TestCrawlAndIngest_CancellationDuringFetch(t *testing.T) {
	fetchStarted := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case fetchStarted <- struct{}{}:
		default:
		}
		// Block until client cancels request context
		<-r.Context().Done()
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "cancel_fetch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := DefaultCrawlOptions()
	opts.AllowPrivate = true
	opts.Timeout = 10 * time.Second

	type result struct {
		docs []*store.Doc
		err  error
	}
	done := make(chan result, 1)

	go func() {
		docs, err := CrawlAndIngestContext(ctx, st, ts.URL, "test-proj", opts)
		done <- result{docs: docs, err: err}
	}()

	select {
	case <-fetchStarted:
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for fetch to start")
	}

	cancel()

	select {
	case res := <-done:
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("expected context.Canceled error, got: %v", res.err)
		}
		if len(res.docs) != 0 {
			t.Fatalf("expected 0 docs, got %d", len(res.docs))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not abort in-flight fetch within bounded 5s deadline")
	}

	docs, err := st.DocsByProject("test-proj", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs in store, found %d", len(docs))
	}
}

func TestCrawlAndIngest_CancellationWhileQueued(t *testing.T) {
	firstStarted := make(chan struct{}, 1)
	var queuedCount int32

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/doc1</loc></url>
  <url><loc>%s/doc2</loc></url>
  <url><loc>%s/doc3</loc></url>
  <url><loc>%s/doc4</loc></url>
  <url><loc>%s/doc5</loc></url>
</urlset>`, ts.URL, ts.URL, ts.URL, ts.URL, ts.URL)
		case "/doc1":
			select {
			case firstStarted <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			w.WriteHeader(http.StatusGatewayTimeout)
		case "/doc2", "/doc3", "/doc4", "/doc5":
			atomic.AddInt32(&queuedCount, 1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "<html><body><h1>Doc Content</h1></body></html>")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "cancel_queued.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := DefaultCrawlOptions()
	opts.AllowPrivate = true
	opts.Concurrency = 1 // Sequential processing to test queue abort
	opts.Timeout = 10 * time.Second

	type result struct {
		docs []*store.Doc
		err  error
	}
	done := make(chan result, 1)

	go func() {
		docs, err := CrawlAndIngestContext(ctx, st, ts.URL, "test-proj", opts)
		done <- result{docs: docs, err: err}
	}()

	select {
	case <-firstStarted:
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for doc1 request")
	}

	cancel()

	select {
	case res := <-done:
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", res.err)
		}
		if len(res.docs) != 0 {
			t.Fatalf("expected 0 docs, got %d", len(res.docs))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not abort queued execution within bounded deadline")
	}

	if q := atomic.LoadInt32(&queuedCount); q != 0 {
		t.Fatalf("expected 0 queued requests executed after cancellation, got %d", q)
	}

	docs, err := st.DocsByProject("test-proj", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs in store, found %d", len(docs))
	}
}
