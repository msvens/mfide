package server

import (
	"net/http"
)

// routes declares every endpoint. Patterns are written without the prefix.
func (s *mserver) routes() {
	s.mGET("/health", s.public(s.handleHealth))
	s.mGET("/key", s.keyed(s.handleKey))
	s.mGET("/v1/players/search", s.keyed(s.handleSearch))
	s.mGET("/v1/players/{fideid}", s.keyed(s.handlePlayer))
	s.mGET("/v1/players/{fideid}/history", s.keyed(s.handleHistory))
	s.mGET("/v1/top", s.keyed(s.handleTop))
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

type keyInfo struct {
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	RatePerMin int    `json:"ratePerMin"`
}

// handleKey lets a consumer check that their key works and what limit it has.
func (s *mserver) handleKey(r *http.Request) (any, error) {
	k := apiKeyFrom(r)
	return keyInfo{Name: k.Name, Prefix: k.Prefix, RatePerMin: k.RatePerMin}, nil
}
