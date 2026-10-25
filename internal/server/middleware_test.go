package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// observedServer is testServer with a logger whose entries the test can read.
func observedServer(keys *fakeKeys, ipRatePerMin int) (*mserver, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	s := newServer("/api", nil, newAuth(keys, time.Minute, ipRatePerMin), nil, zap.New(core).Sugar())
	return s, logs
}

func serve(s *mserver, h http.HandlerFunc, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if key != "" {
		req.Header.Set(apiKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestUnexpectedErrorIsHiddenAndLogged(t *testing.T) {
	s, logs := observedServer(defaultKeys(), 600)
	secret := errors.New(`pq: relation "rating" does not exist`)
	rec := serve(s, s.keyed(func(*http.Request) (any, error) { return nil, secret }), goodKey)

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Err == nil || resp.Err.Code != 500 || resp.Err.Message != "internal server error" {
		t.Errorf("got %+v, want a 500 with a generic message", resp.Err)
	}
	errs := logs.FilterLevelExact(zapcore.ErrorLevel).All()
	if len(errs) != 1 || !strings.Contains(errs[0].ContextMap()["error"].(string), "does not exist") {
		t.Errorf("want one error entry with the real error, got %v", errs)
	}
}

func TestExpectedErrorsAreNotLoggedAsErrors(t *testing.T) {
	s, logs := observedServer(defaultKeys(), 600)
	for name, err := range map[string]error{
		"api error":   BadRequestError("bad q"),
		"no rows":     sql.ErrNoRows,
		"own message": InternalError("deliberate"),
	} {
		rec := serve(s, s.keyed(func(*http.Request) (any, error) { return nil, err }), goodKey)
		if code := errCode(t, rec); code == 500 && name != "own message" {
			t.Errorf("%s: got 500", name)
		}
	}
	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Errorf("got %d error entries, want none", n)
	}
}

func TestOneRequestLinePerAnsweredRequest(t *testing.T) {
	s, logs := observedServer(defaultKeys(), 1)
	ok := func(*http.Request) (any, error) { return "ok", nil }
	serve(s, s.keyed(ok), goodKey)
	for range 10 { // the per-IP burst
		serve(s, s.keyed(ok), "mfide_bad")
	}
	serve(s, s.keyed(ok), "mfide_bad")
	serve(s, s.public(ok), "")

	all := logs.FilterMessage("request").All()
	if len(all) != 13 {
		t.Fatalf("got %d request lines, want 13", len(all))
	}
	lines := append(all[:2:2], all[11:]...) // 200, first 401, 429, health
	want := []struct {
		level  zapcore.Level
		status int64
		key    string
	}{{zapcore.InfoLevel, 200, "chess-app"}, {zapcore.InfoLevel, 401, "-"}, {zapcore.InfoLevel, 429, "-"}, {zapcore.DebugLevel, 200, "-"}}
	if len(lines) != len(want) {
		t.Fatalf("got %d request lines, want %d", len(lines), len(want))
	}
	for i, w := range want {
		f := lines[i].ContextMap()
		if lines[i].Level != w.level || f["status"] != w.status || f["key"] != w.key {
			t.Errorf("line %d: %v %v, want %v %d %s", i, lines[i].Level, f, w.level, w.status, w.key)
		}
	}
}
