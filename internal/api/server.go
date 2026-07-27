package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"pgmanager/internal/auth"
	"pgmanager/internal/config"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

// Server represents the HTTP API server.
type Server struct {
	cfg          *config.Config
	mgr          *project.Manager
	store        meta.Store
	addr         string
	router       *chi.Mux
	socketRouter *chi.Mux
}

// NewServer creates a new API server. The store is used for token lookup and
// must be the same store backing the manager.
func NewServer(cfg *config.Config, mgr *project.Manager, store meta.Store, addr string) *Server {
	if addr == "" {
		addr = cfg.API.BindAddress()
	}
	s := &Server{cfg: cfg, mgr: mgr, store: store, addr: addr}
	s.router = s.buildRouter(s.authMiddleware)
	s.socketRouter = s.buildRouter(s.localAuthMiddleware)
	return s
}

// buildRouter wires the full route table behind the supplied authentication
// middleware. The TCP listener gets bearer-token auth; the unix socket gets
// peer-credential auth. The handlers themselves are identical — they only
// ever read the principal back out of the request context.
func (s *Server) buildRouter(authMW func(http.Handler) http.Handler) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Use(securityHeadersMiddleware)

	if len(s.cfg.API.AllowedOrigins) > 0 {
		r.Use(corsMiddleware(s.cfg.API.AllowedOrigins))
	}

	rateLimiter := NewRateLimiter(100, 200)
	r.Use(rateLimiter.Middleware)

	// Audit log wraps every authenticated request.
	r.Use(auditLogMiddleware)

	r.Get("/api/health", s.healthHandler)

	r.Route("/api", func(r chi.Router) {
		r.Use(authMW)

		// Auth/token management.
		r.Get("/auth/whoami", s.whoami)
		r.Get("/auth/tokens", s.listTokens)
		r.Post("/auth/tokens", s.createToken)
		r.Delete("/auth/tokens/{prefix}", s.revokeToken)

		// Device authorization. The first two are reached before the caller
		// has any credentials — see the bypass in authMiddleware.
		r.Post("/auth/device", s.startDeviceAuth)
		r.Post("/auth/device/token", s.pollDeviceAuth)
		r.Get("/auth/devices", s.listDeviceRequests)
		r.Get("/auth/device/{user_code}", s.getDeviceRequest)
		r.Post("/auth/device/{user_code}/approve", s.approveDeviceRequest)
		r.Post("/auth/device/{user_code}/deny", s.denyDeviceRequest)

		// Projects.
		r.Get("/projects", s.listProjects)
		r.Post("/projects", s.createProject)
		r.Delete("/projects/{name}", s.deleteProject)

		// Databases.
		r.Get("/projects/{name}/databases", s.listDatabases)
		r.Post("/projects/{name}/databases", s.createDatabase)
		r.Get("/projects/{name}/databases/{env}", s.getDatabase)
		r.Get("/projects/{name}/databases/{env}/credentials", s.getDatabaseCredentials)
		r.Delete("/projects/{name}/databases/{env}", s.deleteDatabase)

		r.Post("/cleanup", s.cleanup)
	})

	// Optional static admin UI.
	if webDir := s.webDir(); webDir != "" {
		r.Handle("/*", staticHandler(webDir))
	}

	return r
}

// webDir resolves the directory to serve the admin UI from. Empty means
// "don't serve a UI": either it was explicitly disabled with web_dir: "-"
// (PGMANAGER_WEB_DIR=-) or the directory does not exist.
func (s *Server) webDir() string {
	dir := s.cfg.API.WebDir
	if dir == "-" {
		return ""
	}
	if dir == "" {
		dir = "./web"
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// staticHandler serves the admin UI. Unknown paths fall back to index.html so
// the SPA owns its own routing, but anything under /api is never served from
// disk — an unmatched API path must stay a 404, not silently return HTML.
func staticHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." || clean == "/" || clean == ".." || strings.HasPrefix(clean, "../") {
			http.ServeFile(w, r, index)
			return
		}
		// http.Dir already rejects traversal; this is about choosing between
		// "serve the real file" and "hand the SPA its route".
		if info, err := os.Stat(filepath.Join(dir, clean)); err != nil || info.IsDir() {
			http.ServeFile(w, r, index)
			return
		}
		fs.ServeHTTP(w, r)
	})
}

// Start binds and serves. Returns after graceful shutdown or fatal error.
func (s *Server) Start() error {
	if err := s.bootstrap(); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	// Requests nobody ever completed are just noise; clear them at boot.
	s.purgeExpiredDeviceRequests(context.Background())

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	sockSrv, cleanupSocket, err := s.startSocketServer(serverErrors)
	if err != nil {
		return err
	}
	defer cleanupSocket()

	go func() {
		log.Printf("API server listening on %s", s.addr)
		if strings.HasPrefix(s.addr, "127.") || strings.HasPrefix(s.addr, "localhost") {
			log.Printf("Bound to a local address — front this with a reverse proxy (e.g. Caddy) for TLS")
		}
		serverErrors <- srv.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdown:
		log.Printf("Received %v signal, initiating graceful shutdown...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if sockSrv != nil {
			_ = sockSrv.Shutdown(ctx)
		}
		if err := srv.Shutdown(ctx); err != nil {
			srv.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}
	return nil
}

// startSocketServer brings up the local admin listener when api.socket is
// configured. Callers on the other end of this socket are trusted on the
// strength of filesystem permissions alone — there is no token — so the
// socket is created 0660 and, where configured, owned by a dedicated group.
//
// Returns a nil server (and a no-op cleanup) when no socket is configured.
func (s *Server) startSocketServer(serverErrors chan<- error) (*http.Server, func(), error) {
	path := s.cfg.API.Socket
	if path == "" {
		return nil, func() {}, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, nil, fmt.Errorf("socket directory: %w", err)
	}
	// A leftover socket from an unclean shutdown would otherwise make bind
	// fail with EADDRINUSE forever.
	if info, err := os.Stat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, nil, fmt.Errorf("refusing to replace %s: not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, nil, fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return nil, nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	if group := s.cfg.API.SocketGroup; group != "" {
		if err := chownToGroup(path, group); err != nil {
			ln.Close()
			return nil, nil, fmt.Errorf("chown %s to group %q: %w", path, group, err)
		}
	}

	sockSrv := &http.Server{
		Handler:     s.socketRouter,
		IdleTimeout: 60 * time.Second,
		ConnContext: withPeerCred,
	}
	go func() {
		log.Printf("Local admin socket listening on %s (mode 0660) — callers are trusted as admin", path)
		if err := sockSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("socket server: %w", err)
		}
	}()

	return sockSrv, func() { _ = os.Remove(path) }, nil
}

// chownToGroup sets the socket's group so members can talk to pgmanager
// without being root.
func chownToGroup(path, group string) error {
	g, err := user.LookupGroup(group)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("parse gid %q: %w", g.Gid, err)
	}
	return os.Chown(path, -1, gid)
}

// bootstrap runs once at startup: validates auth config, ensures at least one
// active admin token exists, generating one if needed.
func (s *Server) bootstrap() error {
	if s.cfg.API.RequireToken && s.cfg.API.Token == "" {
		// scoped tokens are required; ensure one exists or generate one
		if err := s.ensureAdminToken(context.Background()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) ensureAdminToken(ctx context.Context) error {
	has, err := s.store.HasActiveAdminToken(ctx)
	if err != nil {
		return fmt.Errorf("check admin token: %w", err)
	}
	if has {
		return nil
	}

	// Optional operator-supplied bootstrap token. Lets ops set the value via
	// systemd/docker env from a secret manager.
	if env := os.Getenv("PGMANAGER_BOOTSTRAP_TOKEN"); env != "" {
		plain := env
		if !strings.HasPrefix(plain, auth.TokenPrefix) {
			return fmt.Errorf("PGMANAGER_BOOTSTRAP_TOKEN must start with %q", auth.TokenPrefix)
		}
		hash := auth.HashToken(plain)
		if err := s.store.CreateToken(ctx, &meta.Token{
			Name:        "bootstrap",
			TokenHash:   hash,
			TokenPrefix: auth.DisplayPrefix(plain),
			Scopes:      []string{auth.ScopeAdmin},
			CreatedBy:   "PGMANAGER_BOOTSTRAP_TOKEN",
		}); err != nil {
			return fmt.Errorf("store bootstrap token: %w", err)
		}
		log.Printf("Registered admin token from PGMANAGER_BOOTSTRAP_TOKEN (prefix %s)", auth.DisplayPrefix(plain))
		return nil
	}

	// Auto-generate.
	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		return err
	}
	if err := s.store.CreateToken(ctx, &meta.Token{
		Name:        "bootstrap",
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      []string{auth.ScopeAdmin},
		CreatedBy:   "auto-bootstrap",
	}); err != nil {
		return fmt.Errorf("store bootstrap token: %w", err)
	}

	path, writeErr := writeBootstrapTokenFile(s.cfg.DataDir, plain)
	if writeErr != nil {
		log.Printf("WARNING: could not write bootstrap token file: %v", writeErr)
		log.Printf("BOOTSTRAP ADMIN TOKEN (save this — it will not be shown again):")
		log.Printf("    %s", plain)
	} else {
		log.Printf("Bootstrap admin token written to %s (mode 0600). Read it once, then `rm` the file.", path)
	}
	return nil
}

func writeBootstrapTokenFile(dataDir, token string) (string, error) {
	if dataDir == "" {
		return "", errors.New("data_dir not configured")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dataDir, "bootstrap-token.txt")
	return path, os.WriteFile(path, []byte(token+"\n"), 0o600)
}

// Router returns the chi router for testing.
func (s *Server) Router() *chi.Mux { return s.router }
