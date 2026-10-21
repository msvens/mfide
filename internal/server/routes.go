package server

import (
	"net/http"
)

// routes declares every endpoint. Patterns are written without the prefix.
func (s *mserver) routes() {
	s.mGET("/health", s.public(s.handleHealth))
}

func (s *mserver) mGET(p string, h http.HandlerFunc) {
	s.r.HandleFunc("GET "+s.prefixPath+p, h)
}

type health struct {
	Status string `json:"status"`
	DB     string `json:"db"`
}

func (s *mserver) handleHealth(_ *http.Request) (any, error) {
	if s.pg == nil {
		return health{Status: "ok", DB: "not configured"}, nil
	}
	if err := s.pg.Ping(); err != nil {
		return nil, ServiceUnavailableError("database unreachable")
	}
	return health{Status: "ok", DB: "ok"}, nil
}
