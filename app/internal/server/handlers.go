package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"gatekeeper/internal/logs"
	"gatekeeper/internal/policy"
)

// handleLogs lists the available build logs, or returns one of them when the
// name query parameter is set.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		names, err := s.logs.List()
		if err != nil {
			s.log.Error("list logs", "error", err.Error())
			writeError(w, http.StatusInternalServerError, "could not list logs")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": names})
		return
	}

	data, err := s.logs.Read(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such log")
		return
	case errors.Is(err, logs.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "log is too large")
		return
	case err != nil:
		s.log.Error("read log", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not read log")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

// handleArchive lists the files inside a log archive.
func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !strings.HasSuffix(name, ".tar.gz") {
		writeError(w, http.StatusBadRequest, "archives are .tar.gz files")
		return
	}
	entries, err := s.logs.ArchiveEntries(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "could not read archive")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleListPolicies returns every tenant's policy.
func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"policies": s.policies.All()})
}

// handleGetPolicy returns one tenant's policy.
func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.policies.Get(r.PathValue("tenant"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown tenant")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handlePutPolicy replaces one tenant's policy and drops cached decisions,
// which may no longer hold under the new rules.
func (s *Server) handlePutPolicy(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	if _, ok := s.policies.Get(tenant); !ok {
		writeError(w, http.StatusNotFound, "unknown tenant")
		return
	}

	var p policy.Policy
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid policy JSON")
		return
	}
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, b := range p.TrustedBuilders {
		if _, ok := s.cfg.BuilderKeys()[b]; !ok {
			writeError(w, http.StatusBadRequest, "policy trusts an unknown builder")
			return
		}
	}

	s.policies.Set(tenant, p)
	s.cache.Flush()
	s.log.Info("policy updated", "tenant", tenant)
	w.WriteHeader(http.StatusNoContent)
}

// handleFlushCache drops every cached decision.
func (s *Server) handleFlushCache(w http.ResponseWriter, r *http.Request) {
	s.cache.Flush()
	w.WriteHeader(http.StatusNoContent)
}

// writeJSON sends v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError sends a JSON error message with the given status code.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
