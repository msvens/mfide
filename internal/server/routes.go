package server

import (
	"net/http"
	"time"
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
	Status       string     `json:"status"`
	DB           string     `json:"db"`
	RatingsMonth string     `json:"ratingsMonth,omitempty"` // the current month, e.g. 2026-10
	LastImport   *time.Time `json:"lastImport,omitempty"`
	LastSync     *syncInfo  `json:"lastSync,omitempty"`
}

type syncInfo struct {
	FinishedAt time.Time `json:"finishedAt"`
	Ok         bool      `json:"ok"`
}

// handleHealth also reports how fresh the data is, so monitoring can notice a
// sync that stopped working.
func (s *mserver) handleHealth(_ *http.Request) (any, error) {
	if s.pg == nil {
		return health{Status: "ok", DB: "not configured"}, nil
	}
	if err := s.pg.Ping(); err != nil {
		return nil, ServiceUnavailableError("database unreachable")
	}
	st, err := s.pg.Status()
	if err != nil {
		return nil, err
	}
	h := health{Status: "ok", DB: "ok", LastImport: st.LastImport}
	if st.RatingsMonth != nil {
		h.RatingsMonth = st.RatingsMonth.Format("2006-01")
	}
	if r := st.LastSync; r != nil && r.FinishedAt != nil {
		h.LastSync = &syncInfo{FinishedAt: *r.FinishedAt, Ok: r.Ok != nil && *r.Ok}
	}
	return h, nil
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
