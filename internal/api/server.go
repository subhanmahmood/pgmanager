package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"pgmanager/internal/config"
	"pgmanager/internal/project"
)

// Server represents the HTTP API server
type Server struct {
	cfg     *config.Config
	mgr     *project.Manager
	port    int
	router  *chi.Mux
}

// NewServer creates a new API server
func NewServer(cfg *config.Config, mgr *project.Manager, port int) *Server {
	s := &Server{
		cfg:  cfg,
		mgr:  mgr,
		port: port,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(60 * time.Second))

	// Health check (no auth required)
	r.Get("/api/health", s.healthHandler)

	// API routes with optional auth
	r.Route("/api", func(r chi.Router) {
		// Apply auth middleware if token is configured
		if s.cfg.API.Token != "" {
			r.Use(s.authMiddleware)
		}

		// Projects
		r.Get("/projects", s.listProjects)
		r.Post("/projects", s.createProject)
		r.Delete("/projects/{name}", s.deleteProject)

		// Databases
		r.Get("/projects/{name}/databases", s.listDatabases)
		r.Post("/projects/{name}/databases", s.createDatabase)
		r.Get("/projects/{name}/databases/{env}", s.getDatabase)
		r.Delete("/projects/{name}/databases/{env}", s.deleteDatabase)

		// Cleanup
		r.Post("/cleanup", s.cleanup)
	})

	// Serve static files for web UI
	webDir := "./web"
	if _, err := os.Stat(webDir); err == nil {
		fileServer := http.FileServer(http.Dir(webDir))
		r.Handle("/*", fileServer)
	}

	s.router = r
}

// Start starts the HTTP server with graceful shutdown
func (s *Server) Start() error {
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Channel to listen for errors from ListenAndServe
	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("API server listening on :%d", s.port)
		serverErrors <- srv.ListenAndServe()
	}()

	// Channel to listen for interrupt signals
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Block until we receive a signal or error
	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		log.Printf("Received %v signal, initiating graceful shutdown...", sig)

		// Create a context with timeout for shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Attempt graceful shutdown
		if err := srv.Shutdown(ctx); err != nil {
			// Force close if graceful shutdown fails
			srv.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}

	return nil
}

// Router returns the chi router for testing
func (s *Server) Router() *chi.Mux {
	return s.router
}
