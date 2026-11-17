// Package server exposes gatekeeper over HTTP.
package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"gatekeeper/internal/attest"
	"gatekeeper/internal/audit"
	"gatekeeper/internal/cache"
	"gatekeeper/internal/config"
	"gatekeeper/internal/logs"
	"gatekeeper/internal/policy"
	"gatekeeper/internal/ratelimit"
)

// Server holds everything the HTTP handlers need.
type Server struct {
	cfg      config.Config
	policies *policy.Store
	verifier *attest.Verifier
	cache    *cache.Cache
	audit    *audit.Log
	logs     *logs.Store
	limiter  *ratelimit.Limiter
	log      *slog.Logger
}

// NewServer builds the HTTP handler for the given settings.
func NewServer(cfg config.Config) (http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	auditLog, err := audit.New(cfg.AuditFile, 1000)
	if err != nil {
		return nil, err
	}

	policies := make(map[string]policy.Policy, len(cfg.Tenants))
	for _, t := range cfg.Tenants {
		policies[t.Name] = t.Policy
	}

	s := &Server{
		cfg:      cfg,
		policies: policy.NewStore(policies),
		verifier: attest.NewVerifier(cfg.BuilderKeys(), cfg.AttestationMaxAge()),
		cache:    cache.New(cfg.CacheTTL()),
		audit:    auditLog,
		logs:     logs.New(cfg.LogDir, cfg.ArchiveDir),
		limiter:  ratelimit.New(cfg.RateLimitPerMinute),
		log:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	return s.routes(), nil
}

// routes wires every endpoint to its handler.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Tenant API: callers authenticate with their tenant token.
	mux.Handle("POST /v1/decide", s.tenantAuth(http.HandlerFunc(s.handleDecide)))
	mux.Handle("GET /v1/audit", s.tenantAuth(http.HandlerFunc(s.handleAudit)))
	mux.Handle("GET /v1/logs", s.tenantAuth(http.HandlerFunc(s.handleLogs)))
	mux.Handle("GET /v1/archives", s.tenantAuth(http.HandlerFunc(s.handleArchive)))

	// Admin API: callers authenticate with the admin token.
	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin/policies", s.handleListPolicies)
	admin.HandleFunc("GET /admin/policies/{tenant}", s.handleGetPolicy)
	admin.HandleFunc("POST /admin/cache/flush", s.handleFlushCache)
	mux.Handle("/admin/", s.adminAuth(admin))

	// Policy updates pushed by the deploy pipeline.
	mux.HandleFunc("PUT /admin/policies/{tenant}", s.handlePutPolicy)

	if s.cfg.Debug {
		mux.HandleFunc("GET /debug/config", s.handleDebugConfig)
	}
	return s.logRequests(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleDebugConfig returns the settings the server is running with. It is
// only registered when debug mode is on.
func (s *Server) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}
