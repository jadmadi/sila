package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// setupTestServer creates a temporary SQLite store and initialized Server.
func setupTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_sila.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	// Insert test project and sample data
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	// Sample message
	_, _, err = st.WriteRun("run-1", "claude", "1", []store.LoadMessage{
		{
			ProjectID:   p.ID,
			ContentHash: "hash-1",
			Role:        "assistant",
			Content:     "Decided to adopt pure-Go SQLite for zero CGO dependencies.",
			TS:          time.Now().UTC().Format(time.RFC3339),
			Tool:        "claude",
		},
	})
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	// Sample fact
	_ = st.UpsertFact(store.Fact{
		ID:            "fact-1",
		ProjectID:     p.ID,
		Claim:         "Pure-Go SQLite driver",
		Kind:          "invariant",
		Confidence:    "invariant",
		FileRefsExist: true,
		RepoFiles:     []string{},
	})

	// Sample feedback
	_ = st.SaveFeedback(&store.Feedback{
		ID:        "fb-1",
		ProjectID: p.ID,
		Category:  "architecture",
		Agent:     "test-agent",
		Content:   "Consider adding SSE for live updates",
		Status:    "open",
	})

	cfg := Config{
		Host:       "127.0.0.1",
		Port:       -1, // ephemeral port: parallel test packages must not contend for 7452
		ProjectDir: dir,
		ProjectID:  p.ID,
		NoOpen:     true,
		Store:      st,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("srv.Start: %v", err)
	}

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = st.Close()
	}

	return srv, cleanup
}

func TestStaticAssetServingAndSPAFallback(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Root index.html
	resp, err := client.Get(srv.URL() + "/")
	if err != nil {
		t.Fatalf("Get /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "sila") {
		t.Errorf("expected body to contain 'sila'")
	}

	// 2. Style CSS
	respCSS, err := client.Get(srv.URL() + "/style.css")
	if err != nil {
		t.Fatalf("Get /style.css: %v", err)
	}
	defer respCSS.Body.Close()
	if respCSS.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for css, got %d", respCSS.StatusCode)
	}

	// 3. App JS
	respJS, err := client.Get(srv.URL() + "/app.js")
	if err != nil {
		t.Fatalf("Get /app.js: %v", err)
	}
	defer respJS.Body.Close()
	if respJS.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for js, got %d", respJS.StatusCode)
	}

	// 4. SPA Fallback for unknown path
	respFallback, err := client.Get(srv.URL() + "/unknown-route")
	if err != nil {
		t.Fatalf("Get /unknown-route: %v", err)
	}
	defer respFallback.Body.Close()
	if respFallback.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK SPA fallback, got %d", respFallback.StatusCode)
	}

	// 5. Unhandled /api/ route returns 404 JSON
	respAPI404, err := client.Get(srv.URL() + "/api/nonexistent")
	if err != nil {
		t.Fatalf("Get /api/nonexistent: %v", err)
	}
	defer respAPI404.Body.Close()
	if respAPI404.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found for unhandled API, got %d", respAPI404.StatusCode)
	}
}

func TestStatusEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL() + "/api/status")
	if err != nil {
		t.Fatalf("Get /api/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode json: %v", err)
	}

	if data["project_id"] != srv.cfg.ProjectID {
		t.Errorf("expected project_id %q, got %v", srv.cfg.ProjectID, data["project_id"])
	}
	if data["messages"].(float64) < 1 {
		t.Errorf("expected at least 1 message, got %v", data["messages"])
	}
	if data["facts"].(float64) < 1 {
		t.Errorf("expected at least 1 fact, got %v", data["facts"])
	}
}

func TestSearchAndExpandEndpoints(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 5 * time.Second}

	// Search
	resp, err := client.Get(srv.URL() + "/api/search?q=SQLite")
	if err != nil {
		t.Fatalf("Get /api/search: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var searchResults []store.StoredMessage
	if err := json.NewDecoder(resp.Body).Decode(&searchResults); err != nil {
		t.Fatalf("decode search results: %v", err)
	}
	if len(searchResults) == 0 {
		t.Fatalf("expected search results for 'SQLite', got none")
	}

	// Expand
	msgID := searchResults[0].ID
	respExpand, err := client.Get(fmt.Sprintf("%s/api/expand?id=%d&window=2", srv.URL(), msgID))
	if err != nil {
		t.Fatalf("Get /api/expand: %v", err)
	}
	defer respExpand.Body.Close()

	if respExpand.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", respExpand.StatusCode)
	}
}

func TestClaimsAndFactsLifecycle(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. GET /api/claims
	resp, err := client.Get(srv.URL() + "/api/claims")
	if err != nil {
		t.Fatalf("Get /api/claims: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 2. POST /api/claims/verify
	respVerify, err := client.Post(srv.URL()+"/api/claims/verify", "application/json", nil)
	if err != nil {
		t.Fatalf("Post /api/claims/verify: %v", err)
	}
	defer respVerify.Body.Close()
	if respVerify.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", respVerify.StatusCode)
	}

	// 3. POST /api/facts (create invariant)
	factPayload := map[string]any{
		"claim": "Never bypass SQLite WAL mode",
		"kind":  "invariant",
		"agent": "test-runner",
	}
	b, _ := json.Marshal(factPayload)
	respFact, err := client.Post(srv.URL()+"/api/facts", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Post /api/facts: %v", err)
	}
	defer respFact.Body.Close()
	if respFact.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 Created, got %d", respFact.StatusCode)
	}
}

func TestFeedbackLifecycle(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. GET /api/feedback
	resp, err := client.Get(srv.URL() + "/api/feedback?status=open")
	if err != nil {
		t.Fatalf("Get /api/feedback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 2. POST /api/feedback (resolve)
	resolveReq := map[string]string{
		"id":    "fb-1",
		"agent": "test-agent",
		"goal":  "web-knowledge-and-decision-interface",
		"note":  "SSE stream implemented cleanly in internal/web/events.go",
	}
	b, _ := json.Marshal(resolveReq)
	respResolve, err := client.Post(srv.URL()+"/api/feedback", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Post /api/feedback (resolve): %v", err)
	}
	defer respResolve.Body.Close()
	if respResolve.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", respResolve.StatusCode)
	}

	// Verify resolved in store
	fb, err := srv.cfg.Store.GetFeedback("fb-1")
	if err != nil || fb.Status != "closed" {
		t.Errorf("expected feedback status 'closed', got %v (err=%v)", fb, err)
	}

	// 3. POST /api/feedback?action=reopen
	reopenReq := map[string]string{"id": "fb-1"}
	bReopen, _ := json.Marshal(reopenReq)
	respReopen, err := client.Post(srv.URL()+"/api/feedback?action=reopen", "application/json", bytes.NewReader(bReopen))
	if err != nil {
		t.Fatalf("Post /api/feedback (reopen): %v", err)
	}
	defer respReopen.Body.Close()
	if respReopen.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", respReopen.StatusCode)
	}

	fbReopened, _ := srv.cfg.Store.GetFeedback("fb-1")
	if fbReopened.Status != "open" {
		t.Errorf("expected status 'open' after reopen, got %s", fbReopened.Status)
	}
}

func TestVisionTriageEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// Insert explicit non-goal into store
	_, _ = srv.cfg.Store.InsertNonGoal(srv.cfg.ProjectID, "Never require external docker daemon or node runtime", "human")

	client := &http.Client{Timeout: 5 * time.Second}
	reqBody := map[string]string{
		"proposal": "Let's require Docker daemon for all agent workflows",
	}
	b, _ := json.Marshal(reqBody)

	resp, err := client.Post(srv.URL()+"/api/vision/triage", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Post /api/vision/triage: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode triage result: %v", err)
	}

	if data["verdict"] != "REJECTED" {
		t.Errorf("expected verdict REJECTED for conflicting proposal, got %v", data["verdict"])
	}
}

func TestSSEEventsStream(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, srv.URL()+"/api/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Connect to SSE: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// Read initial connected event
	buf := make([]byte, 512)
	n, err := resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("read SSE body: %v", err)
	}
	chunk := string(buf[:n])
	if !strings.Contains(chunk, "connected") {
		t.Errorf("expected initial SSE chunk to contain 'connected', got %q", chunk)
	}
}

func TestPortConflictAutomaticFallback(t *testing.T) {
	srv1, cleanup1 := setupTestServer(t)
	defer cleanup1()

	port := srv1.Port()
	if port <= 0 {
		t.Fatalf("invalid server port %d", port)
	}

	// Start server 2 with explicitly the SAME port
	cfg2 := srv1.cfg
	cfg2.Port = port
	srv2, err := NewServer(cfg2)
	if err != nil {
		t.Fatalf("NewServer 2: %v", err)
	}
	if err := srv2.Start(); err != nil {
		t.Fatalf("srv2.Start should fallback gracefully, but errored: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv2.Shutdown(ctx)
	}()

	if srv2.Port() == port {
		t.Errorf("expected srv2 to bind to fallback port, got same port %d", srv2.Port())
	}
	if srv2.Port() <= 0 {
		t.Errorf("invalid fallback port %d", srv2.Port())
	}
}

// TestWebTrustBoundary reproduces the codex-eval F06 probes against the
// middleware: unrelated Host/Origin/non-JSON mutations must be rejected and
// persist nothing, while valid same-origin requests keep working.
func TestWebTrustBoundary(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()
	base := srv.URL()
	// A client of this server only. http.DefaultClient's pool keeps a
	// connection to 127.0.0.1:7452 alive past this server's shutdown, and a
	// later test bound to the same port reuses the dead connection: a POST is
	// not retried, so it fails with EOF.
	client := &http.Client{Timeout: 5 * time.Second}

	post := func(host, origin, contentType string) *http.Response {
		t.Helper()
		body := `{"claim":"boundary probe invariant","kind":"invariant"}`
		req, err := http.NewRequest(http.MethodPost, base+"/api/facts", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// 1. Eval probe: unrelated Host, unrelated Origin, text/plain body.
	resp := post("evil.example.com", "http://evil.example.com", "text/plain")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unrelated Host must be 403, got %d", resp.StatusCode)
	}

	// 2. Loopback Host but unrelated Origin (cross-site CSRF shape).
	resp = post("127.0.0.1", "http://evil.example.com", "text/plain")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unrelated Origin must be 403, got %d", resp.StatusCode)
	}

	// 3. Same-origin Host but non-JSON body.
	resp = post("127.0.0.1", "", "text/plain")
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain mutation must be 415, got %d", resp.StatusCode)
	}

	// 4. Nothing from the rejected probes persisted.
	facts, err := srv.cfg.Store.FactsByProject(srv.cfg.ProjectID)
	if err == nil && facts != nil {
		for _, f := range facts {
			if strings.Contains(f.Claim, "boundary probe") {
				t.Fatalf("rejected probe persisted a fact: %+v", f)
			}
		}
	}

	// 5. Valid same-origin JSON mutation still succeeds.
	resp = post("127.0.0.1", "", "application/json")
	if resp.StatusCode >= 400 {
		t.Fatalf("valid same-origin mutation must succeed, got %d", resp.StatusCode)
	}
}

// TestWebExposeTokenGate verifies non-loopback binding requires --expose and
// that exposed servers gate mutations behind the launch token.
func TestWebExposeTokenGate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Non-loopback bind without Expose is refused.
	if _, err := NewServer(Config{Host: "0.0.0.0", Port: 0, Store: st}); err == nil {
		t.Fatal("non-loopback bind without expose must be refused")
	}

	// 2. Expose semantics are host-driven: a remote bind gates mutations.
	srv, err := NewServer(Config{Host: "192.168.1.5", Port: 0, Expose: true, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if srv.Token() == "" {
		t.Fatal("launch token must be generated")
	}

	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1/api/facts", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("exposed mutation without token must be 401, got %d", rr.Code)
	}

	req, _ = http.NewRequest(http.MethodPost, "http://192.168.1.5/api/facts", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("exposed mutation with token must pass, got %d", rr.Code)
	}

	req, _ = http.NewRequest(http.MethodGet, "http://192.168.1.5/api/status", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reads never require the token, got %d", rr.Code)
	}
}

// TestWebRejectsMissingContentType guards the bypass where a mutation that
// simply omitted Content-Type was accepted.
func TestWebRejectsMissingContentType(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()
	// Scoped client: the shared DefaultClient may hold a pooled connection to
	// this port from a previous test's server, and a POST is not retried.
	client := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequest(http.MethodPost, srv.URL()+"/api/facts", strings.NewReader(`{"claim":"no content type"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Del("Content-Type")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("mutation without Content-Type must be 415, got %d", resp.StatusCode)
	}
}

// TestWebExposedTokenFlow covers the exposed-server path end to end: a request
// addressed to the machine's real address is served (not 403), a valid token
// seeds a launch cookie, and the cookie alone authorizes later mutations.
func TestWebExposedTokenFlow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	srv, err := NewServer(Config{Host: "0.0.0.0", Port: 0, Expose: true, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler = srv.secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// A LAN client addresses the server by the machine's IP, not the bind host.
	mutate := func(withToken bool, cookie *http.Cookie) *httptest.ResponseRecorder {
		req, _ := http.NewRequest(http.MethodPost, "http://192.168.100.29/api/facts", strings.NewReader(`{"a":1}`))
		req.Header.Set("Content-Type", "application/json")
		if withToken {
			q := req.URL.Query()
			q.Set("token", srv.Token())
			req.URL.RawQuery = q.Encode()
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	// 1. Real address is not rejected by the Host allowlist (was 403).
	if rr := mutate(true, nil); rr.Code != http.StatusOK {
		t.Fatalf("exposed server rejected its own address: %d", rr.Code)
	}
	// 2. Without a token the mutation is still refused.
	if rr := mutate(false, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("exposed mutation without token must be 401, got %d", rr.Code)
	}

	// 3. The token request seeds a session cookie...
	rr := mutate(true, nil)
	var seed *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == tokenCookie {
			seed = c
		}
	}
	if seed == nil {
		t.Fatal("launch token did not seed the cookie")
	}
	if seed.SameSite != http.SameSiteStrictMode || !seed.HttpOnly {
		t.Fatalf("cookie not hardened: %+v", seed)
	}

	// 4. ...and the cookie alone authorizes the next mutation, which is what
	// lets the browser cockpit work on an exposed server.
	if rr := mutate(false, seed); rr.Code != http.StatusOK {
		t.Fatalf("cookie did not authorize mutation: %d", rr.Code)
	}
}

// TestWebExposeRejectsForeignHostname keeps the DNS-rebinding defense: a
// hostname we did not bind is still refused even when exposed.
func TestWebExposeRejectsForeignHostname(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	srv, err := NewServer(Config{Host: "192.168.1.5", Port: 0, Expose: true, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req, _ := http.NewRequest(http.MethodGet, "http://evil.example.com/api/status", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign hostname must be 403, got %d", rr.Code)
	}
}

// TestWebPromoteApprovesCandidate guards the authority semantics of the
// cockpit's "Promote to Invariant" action: it must actually approve the
// candidate, not just relabel its kind (which every authority surface ignores).
func TestWebPromoteApprovesCandidate(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()
	client := &http.Client{Timeout: 5 * time.Second}

	fact := store.Fact{
		ID: "cand_web", ProjectID: srv.cfg.ProjectID,
		Claim: "Rule: prefer the purple teapot", Kind: "invariant",
		Confidence: "candidate", Corroboration: "candidate",
		ReviewStatus: store.ReviewCandidate,
	}
	if err := srv.cfg.Store.UpsertFact(fact); err != nil {
		t.Fatal(err)
	}

	body := `{"id":"cand_web"}`
	resp, err := client.Post(srv.URL()+"/api/facts?action=promote", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("promote returned %d", resp.StatusCode)
	}

	facts, err := srv.cfg.Store.FactsByProject(srv.cfg.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if f.ID != "cand_web" {
			continue
		}
		if f.ReviewStatus != store.ReviewApproved {
			t.Fatalf("promote left review_status=%q; the candidate is still non-authoritative", f.ReviewStatus)
		}
		if !f.IsFact() {
			t.Fatalf("promoted fact still does not qualify as a fact: %+v", f)
		}
		return
	}
	t.Fatal("promoted fact missing")
}

// TestWebBoundaryRejectsInvalidHostsOriginsAndMediaTypes replays the re-eval
// F06 table through the real middleware. Each row must fail without minting a
// fact; the prefix-matching and host-only-origin bugs made them all succeed.
func TestWebBoundaryRejectsInvalidHostsOriginsAndMediaTypes(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()
	client := &http.Client{Timeout: 5 * time.Second}

	post := func(host, origin, contentType string) *http.Response {
		t.Helper()
		body := `{"claim":"boundary probe fact","kind":"invariant"}`
		req, err := http.NewRequest(http.MethodPost, srv.URL()+"/api/facts", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	cases := []struct {
		name        string
		host        string
		origin      string
		contentType string
	}{
		// A DNS name that merely starts with 127. is not loopback.
		{"loopback-prefix hostname", "127.attacker.invalid:7452", "", "application/json"},
		{"loopback-prefix host and origin", "127.attacker.invalid:7452", "http://127.attacker.invalid:7452", "application/json"},
		// Same host, different port is a different origin.
		{"origin wrong port", "127.0.0.1:7452", "http://127.0.0.1:9999", "application/json"},
		// Scheme mismatch is a different origin.
		{"origin https against http", "127.0.0.1:7452", "https://127.0.0.1:7452", "application/json"},
		// Different address entirely.
		{"origin other address", "127.0.0.1:7452", "https://192.0.2.1", "application/json"},
		// application/jsonp is not application/json.
		{"jsonp media type", "127.0.0.1:7452", "", "application/jsonp"},
		// "null" is a serialized opaque origin, not an absent one.
		{"null origin", "127.0.0.1:7452", "null", "application/json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := post(c.host, c.origin, c.contentType)
			if resp.StatusCode < 400 {
				t.Fatalf("accepted an invalid request: %d", resp.StatusCode)
			}
		})
	}

	// None of the rejected probes may have written a fact.
	facts, err := srv.cfg.Store.FactsByProject(srv.cfg.ProjectID)
	if err == nil {
		for _, f := range facts {
			if strings.Contains(f.Claim, "boundary probe") {
				t.Fatalf("rejected probe persisted a fact: %+v", f)
			}
		}
	}

	// A genuinely same-origin JSON mutation still succeeds.
	resp := post("127.0.0.1:7452", "http://127.0.0.1:7452", "application/json; charset=utf-8")
	if resp.StatusCode >= 400 {
		t.Fatalf("valid same-origin mutation rejected: %d", resp.StatusCode)
	}
}

// TestSameOriginDefaultPorts covers the serialization browsers use: a default
// port is omitted from the Origin header, so http://127.0.0.1 must match a
// request whose Host is 127.0.0.1:80.
func TestSameOriginDefaultPorts(t *testing.T) {
	cases := []struct {
		name     string
		origin   string
		host     string
		tls      bool
		expected bool
	}{
		{"explicit port", "http://127.0.0.1:7452", "127.0.0.1:7452", false, true},
		{"default http port omitted in origin", "http://127.0.0.1", "127.0.0.1:80", false, true},
		{"default http port omitted in host", "http://127.0.0.1:80", "127.0.0.1", false, true},
		{"default https port omitted", "https://127.0.0.1", "127.0.0.1:443", true, true},
		{"wrong port", "http://127.0.0.1:9999", "127.0.0.1:7452", false, false},
		{"scheme mismatch", "https://127.0.0.1:7452", "127.0.0.1:7452", false, false},
		{"opaque null origin", "null", "127.0.0.1:7452", false, false},
		{"foreign host", "http://audit.invalid:7452", "127.0.0.1:7452", false, false},
		{"localhost default port", "http://localhost", "localhost:80", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameOrigin(c.origin, c.host, c.tls); got != c.expected {
				t.Fatalf("sameOrigin(%q, %q, tls=%v) = %v, want %v", c.origin, c.host, c.tls, got, c.expected)
			}
		})
	}
}

// TestWebFrontendMutationShapesAreAccepted replays the request shapes the
// cockpit's own JavaScript sends. The tightened middleware previously rejected
// the app's shutdown and verify buttons with 415 — a regression that only a
// test through the real middleware with the real header shapes can catch.
func TestWebFrontendMutationShapesAreAccepted(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()
	client := &http.Client{Timeout: 5 * time.Second}

	// Shapes mirror app.js exactly, including the JSON content type added there.
	shapes := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"claims verify", http.MethodPost, "/api/claims/verify", "{}"},
		{"shutdown", http.MethodPost, "/api/shutdown", "{}"},
		{"feedback reopen", http.MethodPost, "/api/feedback?action=reopen", `{"id":"fb-1"}`},
		{"fact promote", http.MethodPost, "/api/facts?action=promote", `{"id":"fact-1"}`},
		{"fact delete", http.MethodDelete, "/api/facts?id=fact-1", ""},
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			req, err := http.NewRequest(sh.method, srv.URL()+sh.path, strings.NewReader(sh.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s must succeed for the cockpit to work; got %d", sh.name, resp.StatusCode)
			}
			if sh.name == "shutdown" {
				// This request really stops the server (that is the behavior
				// under test). Drop pooled connections so a later test cannot
				// reuse one against a recycled port and fail with EOF.
				client.CloseIdleConnections()
			}
		})
	}
}

// TestAppJSMutationsSendJSONContentType is the guard the 415 regression needed:
// the middleware requires a JSON content type on mutations, and nothing else in
// the suite reads the cockpit's own JavaScript, so an app.js fetch that omits
// the header would ship green and break the UI in production.
func TestAppJSMutationsSendJSONContentType(t *testing.T) {
	src, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	text := string(src)

	// Walk each fetch(...) call that declares a mutating method and require the
	// call's own options to carry a Content-Type header.
	re := regexp.MustCompile(`fetch\(\s*([^,)]*)`)
	_ = re
	idx := 0
	mutations := 0
	for {
		i := strings.Index(text[idx:], "fetch(")
		if i < 0 {
			break
		}
		start := idx + i
		// Find the end of this fetch call's argument list (balanced parens).
		depth, j := 0, start+len("fetch")
		for ; j < len(text); j++ {
			switch text[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		call := text[start:min(j+1, len(text))]
		idx = j + 1

		if !strings.Contains(call, "'POST'") && !strings.Contains(call, "'DELETE'") &&
			!strings.Contains(call, "\"POST\"") && !strings.Contains(call, "\"DELETE\"") {
			continue
		}
		mutations++
		if !strings.Contains(call, "Content-Type") {
			t.Errorf("app.js mutation without a JSON content type (middleware answers 415):\n%s", call)
		}
	}
	// Mutations may also go through the shared mutation() helper. The helper
	// must carry the JSON content type, and every direct fetch must still be
	// checked above.
	helperCalls := strings.Count(text, "mutation(")
	helperDef := text[strings.Index(text, "function mutation("):]
	if i := strings.Index(helperDef, "\n}"); i > 0 {
		helperDef = helperDef[:i]
	}
	if helperCalls > 0 {
		if !strings.Contains(helperDef, "Content-Type") || !strings.Contains(helperDef, "application/json") {
			t.Errorf("mutation() helper must set the JSON content type")
		}
	}
	if mutations == 0 && helperCalls == 0 {
		t.Fatal("no mutating fetch calls found in app.js; the scan is broken")
	}
}

func TestConsoleShell(t *testing.T) {
	src, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`data-theme="light"`,
		`data-view-panel="today"`,
		`data-view-panel="timeline"`,
		`data-view-panel="recall"`,
		`data-view-panel="memory"`,
		`data-view-panel="goals"`,
		`data-view-panel="governance"`,
		`id="form-ask"`,
		`id="health-strip"`,
		`id="today-timeline"`,
		`id="handoff-panel"`,
		`id="harness-list"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
	for _, gone := range []string{`class="tab-bar"`, `data-tab="`, "What to do:", "💡"} {
		if strings.Contains(html, gone) {
			t.Errorf("index.html still contains legacy cockpit markup %q", gone)
		}
	}
}

func TestConsoleEscaping(t *testing.T) {
	src, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "function escapeHtml(") {
		t.Fatal("app.js has no escapeHtml helper")
	}
	sensitive := []string{
		"claim", "content", "summary", "resolution", "title", "description",
		"tool", "agent", "branch", "category", "note", "proposal",
		"strategic_fit", "recommended_action", "open_items", "repo_files", "slug",
	}
	re := regexp.MustCompile(`\$\{[^}]*\}`)
	for _, match := range re.FindAllString(text, -1) {
		if strings.Contains(match, "escapeHtml(") || strings.Contains(match, "formatNumber(") ||
			strings.Contains(match, "Math.") || strings.Contains(match, "Number(") {
			continue
		}
		for _, id := range sensitive {
			if regexp.MustCompile(`\b` + id + `\b`).MatchString(match) {
				t.Errorf("unescaped sensitive field %q in template expression %s", id, match)
			}
		}
	}
}

func TestServerInMemoryWithTestServer(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "in_memory_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	cfg := Config{
		Host:       "127.0.0.1",
		Port:       -1,
		ProjectDir: dir,
		ProjectID:  p.ID,
		NoOpen:     true,
		Store:      st,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Create Go 1.27 zero-port-binding in-memory test server
	ts := httptest.NewTestServer(t, srv.Handler())
	client := ts.Client()

	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1/api/status", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("json decode status: %v", err)
	}
	if status["project_id"] != p.ID {
		t.Errorf("project_id = %v, want %v", status["project_id"], p.ID)
	}
}

func TestOverviewEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	resp, err := http.Get(srv.URL() + "/api/overview")
	if err != nil {
		t.Fatalf("GET /api/overview: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out overviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Counts.Messages < 1 {
		t.Errorf("messages = %d, want >= 1", out.Counts.Messages)
	}
	if len(out.Harnesses) < 1 {
		t.Errorf("harnesses = %d, want >= 1", len(out.Harnesses))
	}
	if len(out.Recent) < 1 {
		t.Errorf("recent = %d, want >= 1", len(out.Recent))
	}
	if out.ProjectID == "" {
		t.Error("project_id is empty")
	}
}

func TestRecallEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	resp, err := http.Get(srv.URL() + "/api/recall?q=sqlite")
	if err != nil {
		t.Fatalf("GET /api/recall: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Query string `json:"query"`
		Total int    `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Query != "sqlite" {
		t.Errorf("query = %q, want sqlite", out.Query)
	}
	if out.Total < 1 {
		t.Errorf("total = %d, want >= 1", out.Total)
	}
}

func TestRecallRejectsEmptyQuery(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	resp, err := http.Get(srv.URL() + "/api/recall?q=")
	if err != nil {
		t.Fatalf("GET /api/recall: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
