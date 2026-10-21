package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestHealthWithoutDatabase(t *testing.T) {
	s := newServer("/api", nil, zap.NewNop().Sugar())

	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Data health `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data != (health{Status: "ok", DB: "not configured"}) {
		t.Errorf("data = %+v", resp.Data)
	}
}
