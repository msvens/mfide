package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Response is the envelope every JSON endpoint returns: transport status is
// always 200, the real status lives in error.code.
type Response struct {
	Err  *ApiError `json:"error,omitempty"`
	Data any       `json:"data,omitempty"`
}

type handler func(r *http.Request) (any, error)

// public wraps a handler that needs no auth. Use keyed (auth.go) for data.
// Its requests are logged at debug level: /health is polled by monitoring.
func (s *mserver) public(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		data, err := h(r)
		status := s.response(r, data, err, w)
		s.l.Debugw("request", requestFields(r, status, start, "-")...)
	}
}

// requestFields are the fields of the one log line written per answered request.
func requestFields(r *http.Request, status int, start time.Time, key string) []any {
	return []any{"method", r.Method, "path", r.URL.Path, "status", status,
		"ms", time.Since(start).Milliseconds(), "key", key, "ip", clientIP(r)}
}

// response writes the envelope and returns the status it carries (error.code,
// or 200 with data). Unexpected errors are logged here, since the client only
// sees "internal server error".
func (s *mserver) response(r *http.Request, data any, err error, w http.ResponseWriter) int {
	w.Header().Set("Content-Type", "application/json")
	var resp Response
	switch {
	case err != nil:
		resp = Response{Err: ResolveError(err)}
		var apiErr *ApiError
		if resp.Err.Code == http.StatusInternalServerError && !errors.As(err, &apiErr) {
			s.l.Errorw("internal error", "method", r.Method, "path", r.URL.Path, zap.Error(err))
		}
	case data != nil:
		resp = Response{Data: data}
	default:
		s.l.Errorw("handler returned no payload", "method", r.Method, "path", r.URL.Path)
		resp = Response{Err: InternalError("no payload")}
	}
	// Transport status is 200 except for auth and rate limiting, where proxies
	// and clients act on the real status.
	if resp.Err != nil && (resp.Err.Code == http.StatusUnauthorized || resp.Err.Code == http.StatusTooManyRequests) {
		w.WriteHeader(resp.Err.Code)
	}
	if e := json.NewEncoder(w).Encode(resp); e != nil {
		s.l.Errorw("could not encode response", zap.Error(e))
	}
	if resp.Err != nil {
		return resp.Err.Code
	}
	return http.StatusOK
}

// Var reads a path parameter.
func Var(r *http.Request, name string) string {
	return r.PathValue(name)
}
