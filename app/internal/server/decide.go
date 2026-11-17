package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"gatekeeper/internal/attest"
	"gatekeeper/internal/audit"
	"gatekeeper/internal/cache"
	"gatekeeper/internal/policy"
	"gatekeeper/internal/ref"
)

// maxBodyBytes caps the size of request bodies.
const maxBodyBytes = 1 << 20

// decideRequest asks whether an image may be deployed.
type decideRequest struct {
	// Tenant is optional. Older clients send it; newer ones rely on their
	// token alone.
	Tenant      string              `json:"tenant,omitempty"`
	Image       string              `json:"image"`
	Attestation *attest.Attestation `json:"attestation,omitempty"`
}

// decideResponse is the answer to a decideRequest.
type decideResponse struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"`
	Image  string `json:"image"`
	Cached bool   `json:"cached"`
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	var req decideRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	tenant := req.Tenant
	if tenant == "" {
		tenant = tenantFrom(r.Context())
	}
	pol, ok := s.policies.Get(tenant)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown tenant")
		return
	}

	reference, err := ref.Parse(req.Image, s.cfg.Registry)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	key := cache.Key(reference, req.Attestation)
	if d, ok := s.cache.Get(key); ok {
		s.record(tenant, reference, d)
		writeJSON(w, http.StatusOK, decideResponse{
			Allow: d.Allow, Reason: d.Reason, Image: reference.String(), Cached: true,
		})
		return
	}

	verified := false
	if req.Attestation != nil {
		if err := s.verifier.Verify(*req.Attestation); err != nil {
			s.log.Info("attestation rejected",
				"tenant", tenant, "builder", req.Attestation.Builder, "error", err.Error())
		} else {
			verified = true
		}
	}

	d := policy.Evaluate(pol, reference, req.Attestation, verified)
	s.cache.Put(key, d)
	s.record(tenant, reference, d)
	writeJSON(w, http.StatusOK, decideResponse{
		Allow: d.Allow, Reason: d.Reason, Image: reference.String(),
	})
}

// record adds a decision to the audit trail.
func (s *Server) record(tenant string, reference ref.Reference, d policy.Decision) {
	s.audit.Record(audit.Entry{
		Time:   time.Now().UTC(),
		Tenant: tenant,
		Image:  reference.String(),
		Allow:  d.Allow,
		Reason: d.Reason,
	})
}

// handleAudit returns the calling tenant's most recent decisions.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = n
	}
	entries := s.audit.ForTenant(tenantFrom(r.Context()), limit)
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
