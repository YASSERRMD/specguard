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

	"github.com/YASSERRMD/specguard/internal/adapters/grpc"
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
	mux.HandleFunc("/api/mocks", srv.handleMocksList)
	mux.HandleFunc("/api/mocks/config", srv.handleMocksConfig)
	mux.HandleFunc("/api/mocks/start", srv.handleMocksStart)
	mux.HandleFunc("/api/mocks/stop", srv.handleMocksStop)
	mux.HandleFunc("/api/contract/run", srv.handleContractRun)
	mux.HandleFunc("/api/reports/", srv.handleReports)

	// Serve the static web dashboard assets if built
	if _, err := os.Stat("web/dist"); err == nil {
		mux.Handle("/", http.FileServer(http.Dir("web/dist")))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<h1>Specguard API Server</h1><p>Web dashboard assets not built. Run <code>npm run build</code> in <code>web/</code> first.</p>`))
				return
			}
			http.NotFound(w, r)
		})
	}

	srv.server = &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: srv.loggingMiddleware(srv.corsMiddleware(mux)),
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

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
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
			var err error
			isProto := strings.Contains(req.Raw, "syntax = \"proto") || strings.Contains(req.Raw, "syntax = 'proto") || strings.Contains(req.Raw, "service ")
			if isProto {
				adapter := grpc.NewAdapter()
				spec, err = adapter.LoadSpec([]byte(req.Raw))
			} else {
				adapter := rest.NewAdapter()
				spec, err = adapter.LoadSpec([]byte(req.Raw))
			}
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
		id := r.URL.Query().Get("id")
		if id != "" {
			spec, err := s.store.LoadSpec(id)
			if err != nil {
				s.logger.Error("failed to load spec", "id", id, "error", err)
				s.writeError(w, http.StatusNotFound, fmt.Sprintf("Specification %q not found", id))
				return
			}
			s.writeJSON(w, http.StatusOK, spec)
			return
		}

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

func (s *Server) handleMocksList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	s.mocksMu.Lock()
	defer s.mocksMu.Unlock()

	result := make(map[string]string)
	for id, m := range s.mocks {
		result[id] = m.GetAddress()
	}

	s.writeJSON(w, http.StatusOK, result)
}

type mockConfigReq struct {
	ID     string           `json:"id"`
	Config *core.MockConfig `json:"config"`
}

func (s *Server) handleMocksConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req mockConfigReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		if req.ID == "" || req.Config == nil {
			s.writeError(w, http.StatusBadRequest, "Missing ID or config")
			return
		}
		if err := s.store.SaveMockConfig(req.ID, req.Config); err != nil {
			s.logger.Error("failed to save mock config", "id", req.ID, "error", err)
			s.writeError(w, http.StatusInternalServerError, "Failed to save mock config")
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})

	case http.MethodGet:
		id := r.URL.Query().Get("id")
		if id == "" {
			s.writeError(w, http.StatusBadRequest, "Missing id parameter")
			return
		}
		cfg, err := s.store.LoadMockConfig(id)
		if err != nil {
			cfg = &core.MockConfig{
				Host: "127.0.0.1",
				Port: 0,
			}
		}
		s.writeJSON(w, http.StatusOK, cfg)

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

	var adapter core.ProtocolAdapter = rest.NewAdapter()
	isGRPC := false
	for _, op := range spec.Operations {
		if op.Metadata != nil && op.Metadata["protocol"] == "grpc" {
			isGRPC = true
			break
		}
	}
	if isGRPC {
		adapter = grpc.NewAdapter()
	}

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

type contractRunRequest struct {
	ID        string `json:"id"`
	TargetURL string `json:"target_url"`
}

func (s *Server) handleContractRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req contractRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if req.ID == "" || req.TargetURL == "" {
		s.writeError(w, http.StatusBadRequest, "Missing spec id or target_url")
		return
	}

	spec, err := s.store.LoadSpec(req.ID)
	if err != nil {
		s.logger.Error("failed to load spec for contract run", "id", req.ID, "error", err)
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("Specification %q not found", req.ID))
		return
	}

	var adapter core.ProtocolAdapter = rest.NewAdapter()
	isGRPC := false
	for _, op := range spec.Operations {
		if op.Metadata != nil && op.Metadata["protocol"] == "grpc" {
			isGRPC = true
			break
		}
	}
	if isGRPC {
		adapter = grpc.NewAdapter()
	}

	result, err := adapter.RunContractChecks(spec, req.TargetURL)
	if err != nil {
		s.logger.Error("contract validation failed with execution error", "id", req.ID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Contract check error: %v", err))
		return
	}

	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	run := &store.ContractRun{
		ID:          runID,
		SpecID:      req.ID,
		TargetURL:   req.TargetURL,
		Passed:      result.Passed,
		DriftReport: result.DriftReport,
		CreatedAt:   time.Now(),
	}

	if err := s.store.SaveContractRun(run); err != nil {
		s.logger.Error("failed to save contract run details", "id", req.ID, "run_id", runID, "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to save contract run details")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"run_id":       runID,
		"status":       "completed",
		"passed":       result.Passed,
		"drift_report": result.DriftReport,
	})
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Route format: /api/reports/{id} or /api/reports/?spec_id={spec_id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 || parts[3] == "" {
		specID := r.URL.Query().Get("spec_id")
		if specID != "" {
			runs, err := s.store.ListContractRuns(specID)
			if err != nil {
				s.logger.Error("failed to list contract runs", "spec_id", specID, "error", err)
				s.writeError(w, http.StatusInternalServerError, "Failed to list contract runs")
				return
			}
			if runs == nil {
				runs = []store.ContractRun{}
			}
			s.writeJSON(w, http.StatusOK, runs)
			return
		}
		s.writeError(w, http.StatusBadRequest, "Missing report run id or spec_id query parameter")
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
