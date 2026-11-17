package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

type ctxKey int

const tenantKey ctxKey = iota

// bearerToken returns the token from an "Authorization: Bearer" header, or
// the empty string when there is none.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// tenantForToken returns the name of the tenant that owns token.
func (s *Server) tenantForToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	found := ""
	for _, t := range s.cfg.Tenants {
		if subtle.ConstantTimeCompare([]byte(t.Token), []byte(token)) == 1 {
			found = t.Name
		}
	}
	return found, found != ""
}

// tenantAuth admits requests that carry a valid tenant token and records
// which tenant is calling.
func (s *Server) tenantAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := s.tenantForToken(bearerToken(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "missing or invalid tenant token")
			return
		}
		if !s.limiter.Allow(name) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		ctx := context.WithValue(r.Context(), tenantKey, name)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tenantFrom returns the tenant that tenantAuth authenticated.
func tenantFrom(ctx context.Context) string {
	name, _ := ctx.Value(tenantKey).(string)
	return name
}

// adminAuth admits requests that carry the admin token.
func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" || subtle.ConstantTimeCompare([]byte(s.cfg.AdminToken), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or invalid admin token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder remembers the status code a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests writes one line per request. It never logs headers or bodies.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).String(),
		)
	})
}
