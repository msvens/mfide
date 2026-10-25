package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/msvens/mfide/api"
	"github.com/msvens/mfide/internal/dao"
	"go.uber.org/zap"
)

func TestHealthWithoutDatabase(t *testing.T) {
	s := newServer("/api", nil, nil, nil, zap.NewNop().Sugar())

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
	if resp.Data != (health{Status: "ok", DB: "not configured", ApiVersion: api.Version(), Build: build}) {
		t.Errorf("data = %+v", resp.Data)
	}
}

// fakeStatus is a statusStore.
type fakeStatus struct{ status dao.Status }

func (f *fakeStatus) Ping() error                  { return nil }
func (f *fakeStatus) Status() (*dao.Status, error) { return &f.status, nil }

func TestHealthReportsFreshness(t *testing.T) {
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	imported := time.Date(2026, 10, 24, 3, 1, 0, 0, time.UTC)
	ok := false
	st := &fakeStatus{dao.Status{RatingsMonth: &month, LastImport: &imported,
		LastSync: &dao.SyncRun{FinishedAt: &imported, Ok: &ok}}}
	s := newServer("/api", st, nil, nil, zap.NewNop().Sugar())

	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := resp.Data
	sync, _ := d["lastSync"].(map[string]any)
	if d["db"] != "ok" || d["ratingsMonth"] != "2026-10" || d["lastImport"] != "2026-10-24T03:01:00Z" ||
		sync["ok"] != false || sync["finishedAt"] != "2026-10-24T03:01:00Z" {
		t.Errorf("health = %v", d)
	}
}
