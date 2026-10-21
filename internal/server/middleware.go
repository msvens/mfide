package server

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// Response is the envelope every JSON endpoint returns: transport status is
// always 200, the real status lives in error.code.
type Response struct {
	Err  *ApiError `json:"error,omitempty"`
	Data any       `json:"data,omitempty"`
}

type handler func(r *http.Request) (any, error)

// public wraps a handler that needs no auth.
func (s *mserver) public(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := h(r)
		s.response(data, err, w)
	}
}

func (s *mserver) response(data any, err error, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	var resp Response
	switch {
	case err != nil:
		resp = Response{Err: ResolveError(err)}
	case data != nil:
		resp = Response{Data: data}
	default:
		resp = Response{Err: InternalError("no payload")}
	}
	if e := json.NewEncoder(w).Encode(resp); e != nil {
		s.l.Errorw("could not encode response", zap.Error(e))
	}
}

// Var reads a path parameter.
func Var(r *http.Request, name string) string {
	return r.PathValue(name)
}
