package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	cfg    *config.Config
	mgr    *project.Manager
	store  meta.Store
	addr   string
	router *chi.Mux
}

// NewServer creates a new API server. The store is used for token lookup and
// must be the same store backing the manager.
func NewServer(cfg *config.Config, mgr *project.Manager, store meta.Store, addr string) *Server {
	if addr == "" {
		addr = cfg.API.BindAddress()
	}
	s := &Server{cfg: cfg, mgr: mgr, store: store, addr: addr}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
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
		r.Use(s.authMiddleware)

		// Auth/token management.
		r.Get("/auth/whoami", s.whoami)
		r.Get("/auth/tokens", s.listTokens)
		r.Post("/auth/tokens", s.createToken)
		r.Delete("/auth/tokens/{prefix}", s.revokeToken)

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

	// Optional static web UI.
	webDir := "./web"
	if _, err := os.Stat(webDir); err == nil {
		fileServer := http.FileServer(http.Dir(webDir))
		r.Handle("/*", fileServer)
	}

	s.router = r
}

// Start binds and serves. Returns after graceful shutdown or fatal error.
func (s *Server) Start() error {
	if err := s.bootstrap(); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErrors := make(chan error, 1)
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
		if err := srv.Shutdown(ctx); err != nil {
			srv.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}
	return nil
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
