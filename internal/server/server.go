package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/YASSERRMD/specguard/internal/adapters/rest"
	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/YASSERRMD/specguard/internal/store"
)

// Server coordinates HTTP API requests.
type Server struct {
	config  *Config
	store   store.Store
	logger  *slog.Logger
	server  *http.Server
	mocks   map[string]core.RunnableMock
	mocksMu sync.Mutex
}

// NewServer creates a configured HTTP API server instance.
func NewServer(cfg *Config, dbStore store.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	srv := &Server{
		config: cfg,
		store:  dbStore,
		logger: logger,
		mocks:  make(map[string]core.RunnableMock),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/api/specs", srv.handleSpecs)
	mux.HandleFunc("/api/mocks/start", srv.handleMocksStart)
	mux.HandleFunc("/api/mocks/stop", srv.handleMocksStop)
	mux.HandleFunc("/api/contract/run", srv.handleContractRun)
	mux.HandleFunc("/api/reports/", srv.handleReports)

	srv.server = &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: srv.loggingMiddleware(mux),
	}

	return srv
}

// Start launches the HTTP server and blocks until stopped.
func (s *Server) Start() error {
	s.logger.Info("starting server", "port", s.config.Port)
	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// GetAddress returns the actual address the server is listening on.
func (s *Server) GetAddress() string {
	return s.server.Addr
}

// Handler returns the HTTP handler of the server.
func (s *Server) Handler() http.Handler {
	return s.server.Handler
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("shutting down server")
	s.mocksMu.Lock()
	for id, m := range s.mocks {
		s.logger.Info("stopping active mock server", "id", id)
		_ = m.Stop()
	}
	s.mocksMu.Unlock()
	return s.server.Shutdown(ctx)
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("request processed",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start),
		)
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, val interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(val)
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type uploadSpecRequest struct {
	ID   string               `json:"id"`
	Spec *core.NormalizedSpec `json:"spec,omitempty"`
	Raw  string               `json:"raw,omitempty"`
}

func (s *Server) handleSpecs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req uploadSpecRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}

		if req.ID == "" {
			s.writeError(w, http.StatusBadRequest, "Missing spec id")
			return
		}

		var spec *core.NormalizedSpec
		if req.Raw != "" {
			adapter := rest.NewAdapter()
			var err error
			spec, err = adapter.LoadSpec([]byte(req.Raw))
			if err != nil {
				s.logger.Error("failed to parse spec", "id", req.ID, "error", err)
				s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse spec: %v", err))
				return
			}
		} else if req.Spec != nil {
			spec = req.Spec
		} else {
			s.writeError(w, http.StatusBadRequest, "Missing spec or raw content")
			return
		}

		if err := s.store.SaveSpec(req.ID, spec); err != nil {
			s.logger.Error("failed to save spec", "id", req.ID, "error", err)
			s.writeError(w, http.StatusInternalServerError, "Failed to save spec")
			return
		}

		s.writeJSON(w, http.StatusCreated, map[string]string{"id": req.ID, "status": "saved"})

	case http.MethodGet:
		specs, err := s.store.ListSpecs()
		if err != nil {
			s.logger.Error("failed to list specs", "error", err)
			s.writeError(w, http.StatusInternalServerError, "Failed to list specs")
			return
		}
		if specs == nil {
			specs = []string{}
		}
		s.writeJSON(w, http.StatusOK, specs)

	default:
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

type mockRequest struct {
	ID string `json:"id"`
}

func (s *Server) handleMocksStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req mockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if req.ID == "" {
		s.writeError(w, http.StatusBadRequest, "Missing spec id")
		return
	}

	spec, err := s.store.LoadSpec(req.ID)
	if err != nil {
		s.logger.Error("failed to load spec for mock", "id", req.ID, "error", err)
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("Specification %q not found", req.ID))
		return
	}

	mockCfg, err := s.store.LoadMockConfig(req.ID)
	if err != nil {
		mockCfg = &core.MockConfig{
			Host: "127.0.0.1",
			Port: 0,
		}
	}

	s.mocksMu.Lock()
	defer s.mocksMu.Unlock()

	if existing, running := s.mocks[req.ID]; running {
		_ = existing.Stop()
		delete(s.mocks, req.ID)
	}

	adapter := rest.NewAdapter()
	runnableMock, err := adapter.GenerateMock(spec, *mockCfg)
	if err != nil {
		s.logger.Error("failed to generate mock server", "id", req.ID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate mock: %v", err))
		return
	}

	if err := runnableMock.Start(); err != nil {
		s.logger.Error("failed to start mock server", "id", req.ID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to start mock: %v", err))
		return
	}

	s.mocks[req.ID] = runnableMock

	s.writeJSON(w, http.StatusOK, map[string]string{
		"id":      req.ID,
		"status":  "started",
		"address": runnableMock.GetAddress(),
	})
}

func (s *Server) handleMocksStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req mockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if req.ID == "" {
		s.writeError(w, http.StatusBadRequest, "Missing spec id")
		return
	}

	s.mocksMu.Lock()
	defer s.mocksMu.Unlock()

	runnableMock, running := s.mocks[req.ID]
	if !running {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("No running mock server found for spec %q", req.ID))
		return
	}

	if err := runnableMock.Stop(); err != nil {
		s.logger.Error("failed to stop mock server", "id", req.ID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to stop mock: %v", err))
		return
	}

	delete(s.mocks, req.ID)

	s.writeJSON(w, http.StatusOK, map[string]string{
		"id":     req.ID,
		"status": "stopped",
	})
}

func (s *Server) handleContractRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	s.writeError(w, http.StatusNotImplemented, "protocol-specific contract runner not implemented")
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Route format: /api/reports/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 || parts[3] == "" {
		s.writeError(w, http.StatusBadRequest, "Missing report run id")
		return
	}
	runID := parts[3]

	run, err := s.store.GetContractRun(runID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("report run not found: %s", runID))
		return
	}

	s.writeJSON(w, http.StatusOK, run.DriftReport)
}
