package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
)

//go:embed static/*
var staticFS embed.FS

// maxBodyBytes bounds every request body: the local API has no need for
// large uploads, and unbounded bodies are a denial-of-service surface.
const maxBodyBytes = 1 << 20 // 1 MiB

// Config holds runtime options for the embedded web server.
type Config struct {
	Host       string
	Port       int
	ProjectDir string
	ProjectID  string
	NoOpen     bool
	Expose     bool // authorize a non-loopback bind (mutations then require Token)
	Store      *store.Store
	OnShutdown func()
}

// Server embeds Sila's decision cockpit and REST/SSE API.
type Server struct {
	cfg        Config
	events     *EventBroker
	listener   net.Listener
	httpServer *http.Server
	actualPort int
	cancel     context.CancelFunc
	mu         sync.Mutex
	token      string
}

// HostIsRemote reports whether a bind host reaches beyond loopback. Remote
// binds require Expose and gate mutations behind the launch token.
func HostIsRemote(host string) bool {
	return !isLoopbackHost(host)
}

// isLoopbackHost reports whether h (Host header or bind host, port stripped)
// is a loopback name or address. Addresses are parsed, never prefix-matched: a
// DNS name such as 127.attacker.invalid must not pass as loopback.
func isLoopbackHost(h string) bool {
	switch h {
	case "localhost", "::1", "[::1]":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// hostAllowedIn reports whether a request's Host header may be served. The
// allowlist defends against DNS rebinding: a hostname we did not bind is
// rejected. IP literals are always allowed — they cannot be produced by a
// rebinding attack, and an exposed server is reached by whatever address the
// client used (a LAN IP, a container address, or the machine's own name).
func (s *Server) hostAllowedIn(hostHeader string) bool {
	h := strings.Trim(hostOnly(hostHeader), "[]")
	if h == "" {
		return true
	}
	// IP literals cannot be produced by a rebinding attack, and an exposed
	// server is reached by whatever address the client used.
	if net.ParseIP(h) != nil {
		return true
	}
	if isLoopbackHost(h) {
		return true
	}
	return strings.EqualFold(h, strings.Trim(hostOnly(s.cfg.Host), "[]"))
}

// sameOrigin reports whether an Origin header names the same origin as the
// request it accompanies. Origins are compared as scheme + host + port, per
// the same-origin definition: matching only the host would accept an alternate
// scheme or port.
func sameOrigin(origin, requestHost string, requestTLS bool) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	scheme := "http"
	if requestTLS {
		scheme = "https"
	}
	if !strings.EqualFold(u.Scheme, scheme) {
		return false
	}
	// Browsers omit a default port when serializing an origin, so compare
	// host:port with the default filled in on both sides.
	return strings.EqualFold(normalizeHostPort(u.Host, scheme), normalizeHostPort(requestHost, scheme))
}

// normalizeHostPort appends the scheme's default port when the host omits one,
// so "127.0.0.1" and "127.0.0.1:80" compare equal for http.
func normalizeHostPort(hostPort, scheme string) string {
	if _, _, err := net.SplitHostPort(hostPort); err == nil {
		return hostPort
	}
	def := "80"
	if scheme == "https" {
		def = "443"
	}
	return net.JoinHostPort(strings.Trim(hostPort, "[]"), def)
}

func hostOnly(hostHeader string) string {
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		return h
	}
	return hostHeader
}

// newLaunchToken returns a random 256-bit hex token for exposed servers.
func newLaunchToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is unrecoverable for a security token; refuse
		// to serve a predictable one.
		return ""
	}
	return hex.EncodeToString(b)
}

// NewServer initializes a new embedded web server with routes and SSE broker.
// Non-loopback binds are refused unless cfg.Expose is set; exposed servers
// require the per-launch token for privileged mutations.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	// Port semantics: 0 means "unset, use the documented default", -1 means
	// "ask the OS for an ephemeral port". Tests use -1 so parallel test
	// packages never contend for the fixed default port; Start records the
	// assigned port and URL reports it.
	if cfg.Port == -1 {
		cfg.Port = 0
	} else if cfg.Port <= 0 {
		cfg.Port = 7452
	}
	if cfg.ProjectID == "" {
		cfg.ProjectID = "sila"
	}
	exposed := !isLoopbackHost(cfg.Host)
	if exposed && !cfg.Expose {
		return nil, fmt.Errorf("refusing to bind non-loopback host %q without explicit expose; keep the default 127.0.0.1 or pass --expose to accept the risk", cfg.Host)
	}

	s := &Server{
		cfg:    cfg,
		events: NewEventBroker(),
		token:  newLaunchToken(),
	}

	mux := http.NewServeMux()

	// SSE stream
	mux.Handle("/api/events", s.events)

	// REST API endpoints
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/overview", s.handleOverview)
	mux.HandleFunc("/api/recall", s.handleRecall)
	mux.HandleFunc("/api/shutdown", s.handleShutdown)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/expand", s.handleExpand)
	mux.HandleFunc("/api/claims", s.handleClaims)
	mux.HandleFunc("/api/claims/verify", s.handleClaims)
	mux.HandleFunc("/api/facts", s.handleFacts)
	mux.HandleFunc("/api/feedback", s.handleFeedback)
	mux.HandleFunc("/api/adrs", s.handleADRs)
	mux.HandleFunc("/api/vision", s.handleVision)
	mux.HandleFunc("/api/vision/triage", s.handleVisionTriage)
	mux.HandleFunc("/api/goals", s.handleGoals)
	mux.HandleFunc("/api/crew", s.handleCrew)

	// MCP & AgentReady Discovery standards (SEP-1649, RFC 9727, RFC 8288, llms.txt)
	mux.HandleFunc("/.well-known/mcp/server-card.json", s.handleMCPServerCard)
	mux.HandleFunc("/.well-known/linkset.json", s.handleLinkset)
	mux.HandleFunc("/.well-known/linkset", s.handleLinkset)
	mux.HandleFunc("/llms.txt", s.handleLLMsTxt)

	// Static assets with SPA fallback
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("sub static assets: %w", err)
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", mcp.DefaultLinkHeaders(getRequestBaseURL(r)))

		// If path starts with /api/, it was unhandled -> 404
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "api endpoint not found")
			return
		}

		// Try opening file directly from embedded static FS
		cleanPath := strings.TrimPrefix(r.URL.Path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		if _, err := sub.Open(cleanPath); err != nil {
			// SPA fallback to index.html
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})

	s.httpServer = &http.Server{
		Handler:      s.secure(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 for SSE streaming support
		IdleTimeout:  120 * time.Second,
	}

	return s, nil
}

// Handler returns the HTTP handler wrapped with security middleware, suitable for in-memory testing.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// secure wraps the route mux with the local trust boundary: a Host allowlist
// (DNS-rebinding defense, applied to reads and writes), same-origin Origin
// enforcement, JSON content-type on mutations, bounded bodies, and — for
// exposed servers — the per-launch token on privileged mutations.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bound every request body.
		if r.Body != nil {
			r.Body = io.NopCloser(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		}

		// 1. Host allowlist: reject hostnames we did not bind (DNS-rebinding
		// defense). IP literals and the configured host are accepted.
		if !s.hostAllowedIn(r.Host) {
			writeError(w, http.StatusForbidden, "unrecognized host header")
			return
		}

		// 2. Origin: browsers attach it on cross-site requests. This is a real
		// origin comparison — scheme, host, and port must all match the
		// request's own origin — so an alternate port, scheme, or address is
		// rejected even when the host name would otherwise be allowed.
		// "null" is a serialized opaque origin (sandboxed frames, some redirect
		// chains). It is not "no origin": it must fail like any other foreign
		// origin rather than being waved through.
		if origin := r.Header.Get("Origin"); origin != "" {
			if !sameOrigin(origin, r.Host, r.TLS != nil) {
				writeError(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
		}

		// 2b. On an exposed server the launch token is evaluated for every
		// request, not just mutations: the browser lands on a plain GET when it
		// opens the printed URL, and that response must seed the launch cookie
		// so later cockpit actions work without pasting the token.
		hasToken := !s.exposed() || s.requestHasToken(w, r)

		// 3. Mutations: JSON bodies only, and the launch token when exposed.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			// Parse the media type rather than prefix-matching it: a missing
			// header must not bypass the check, and application/jsonp is not
			// application/json. Parameters such as charset=utf-8 are fine.
			mediaType, _, mtErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mtErr != nil || mediaType != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "mutations require a JSON content type")
				return
			}
			if !hasToken {
				writeError(w, http.StatusUnauthorized, "valid launch token required for mutations on an exposed server")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// exposed reports whether the server is bound beyond loopback.
func (s *Server) exposed() bool {
	return !isLoopbackHost(s.cfg.Host)
}

// tokenCookie is how a browser keeps the launch token after opening the URL
// printed at startup. SameSite=Strict keeps it from riding along on requests
// another site initiates.
const tokenCookie = "sila_token"

// requestHasToken checks the Authorization header, X-Sila-Token header, token
// query parameter, or launch cookie against the per-launch token in constant
// time. A valid ?token= also seeds the cookie so the cockpit UI, which cannot
// read the printed token, can mutate for the rest of the session.
func (s *Server) requestHasToken(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	token := s.token
	s.mu.Unlock()
	if token == "" {
		return false
	}
	valid := func(candidate string) bool {
		if candidate == "" {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1
	}
	if q := r.URL.Query().Get("token"); q != "" {
		if !valid(q) {
			return false
		}
		if w != nil {
			http.SetCookie(w, &http.Cookie{
				Name:     tokenCookie,
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
		}
		return true
	}
	if h := r.Header.Get("X-Sila-Token"); valid(h) {
		return true
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		if valid(strings.TrimSpace(auth[len("bearer "):])) {
			return true
		}
	}
	if c, err := r.Cookie(tokenCookie); err == nil && valid(c.Value) {
		return true
	}
	return false
}

// Token returns the per-launch token guarding mutations on exposed servers.
func (s *Server) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// Start binds the listener (with fallback to an available port) and starts serving.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Fallback to ephemeral port on port collision
		fallbackAddr := fmt.Sprintf("%s:0", s.cfg.Host)
		ln, err = net.Listen("tcp", fallbackAddr)
		if err != nil {
			return fmt.Errorf("bind fallback listener: %w", err)
		}
	}

	s.listener = ln
	s.actualPort = ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.events.Start(ctx)

	go func() {
		_ = s.httpServer.Serve(ln)
	}()

	return nil
}

// Port returns the bound TCP port.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actualPort
}

// URL returns the full base URL for the running server.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s:%d", s.cfg.Host, s.Port())
}

// Events returns the SSE broker instance.
func (s *Server) Events() *EventBroker {
	return s.events
}

// Shutdown gracefully shuts down the HTTP server and event broker.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
