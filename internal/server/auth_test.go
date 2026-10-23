package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/msvens/mfide/internal/dao"
	"go.uber.org/zap"
)

// fakeKeys is a keyStore over plaintext keys, counting lookups.
type fakeKeys struct {
	keys    map[string]*dao.ApiKey // by hash
	lookups int
}

func newFakeKeys(plain map[string]*dao.ApiKey) *fakeKeys {
	f := &fakeKeys{keys: map[string]*dao.ApiKey{}}
	for p, k := range plain {
		f.keys[dao.HashKey(p)] = k
	}
	return f
}

func (f *fakeKeys) ByHash(hash string) (*dao.ApiKey, error) {
	f.lookups++
	if k, ok := f.keys[hash]; ok {
		return k, nil
	}
	return nil, sql.ErrNoRows
}

const goodKey = "mfide_good"

func testServer(keys *fakeKeys, ipRatePerMin int) *mserver {
	return newServer("/api", nil, newAuth(keys, time.Minute, ipRatePerMin), nil, zap.NewNop().Sugar())
}

func defaultKeys() *fakeKeys {
	return newFakeKeys(map[string]*dao.ApiKey{
		goodKey: {Id: 1, Name: "chess-app", Prefix: "good", RatePerMin: 600},
	})
}

func get(s *mserver, path, key string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set(apiKeyHeader, key)
	}
	for _, m := range mod {
		m(req)
	}
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Err == nil {
		return 0
	}
	return resp.Err.Code
}

func TestKeyedRejectsMissingAndBadKeys(t *testing.T) {
	s := testServer(defaultKeys(), 600)
	for name, key := range map[string]string{"missing": "", "unknown": "mfide_bad"} {
		rec := get(s, "/api/key", key)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s key: transport status %d, want 401", name, rec.Code)
		}
		if c := errCode(t, rec); c != http.StatusUnauthorized {
			t.Errorf("%s key: error.code %d, want 401", name, c)
		}
	}
}

func TestKeyedAcceptsValidKey(t *testing.T) {
	s := testServer(defaultKeys(), 600)
	rec := get(s, "/api/key", goodKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var resp struct {
		Data keyInfo `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data != (keyInfo{Name: "chess-app", Prefix: "good", RatePerMin: 600}) {
		t.Errorf("data = %+v", resp.Data)
	}
}

func TestHealthNeedsNoKey(t *testing.T) {
	s := testServer(defaultKeys(), 600)
	if rec := get(s, "/api/health", ""); rec.Code != http.StatusOK || errCode(t, rec) != 0 {
		t.Errorf("health without key: status %d", rec.Code)
	}
}

func TestKeyRateLimit(t *testing.T) {
	// 60/min gives the minimum burst of 10.
	keys := newFakeKeys(map[string]*dao.ApiKey{goodKey: {Id: 1, Name: "slow", RatePerMin: 60}})
	s := testServer(keys, 600)
	for i := 0; i < 10; i++ {
		if rec := get(s, "/api/key", goodKey); rec.Code != http.StatusOK {
			t.Fatalf("request %d within burst: status %d", i, rec.Code)
		}
	}
	rec := get(s, "/api/key", goodKey)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over burst: status %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 should carry Retry-After")
	}
	if c := errCode(t, rec); c != http.StatusTooManyRequests {
		t.Errorf("error.code %d, want 429", c)
	}
}

func TestIPLimitOnlyCountsInvalidKeys(t *testing.T) {
	s := testServer(defaultKeys(), 60) // burst 10
	for i := 0; i < 10; i++ {
		if rec := get(s, "/api/key", "mfide_bad"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("bad key %d: status %d, want 401", i, rec.Code)
		}
	}
	if rec := get(s, "/api/key", "mfide_bad"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("bad keys over the IP limit: status %d, want 429", rec.Code)
	}
	// A valid key from the same IP is limited by its key, not the IP.
	if rec := get(s, "/api/key", goodKey); rec.Code != http.StatusOK {
		t.Errorf("valid key after IP limit tripped: status %d, want 200", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		remote, realIP, want string
	}{
		{"203.0.113.5:4000", "", "203.0.113.5"},
		{"203.0.113.5:4000", "198.51.100.1", "203.0.113.5"}, // forged from outside
		{"127.0.0.1:4000", "198.51.100.1", "198.51.100.1"},  // from our nginx
		{"[::1]:4000", "198.51.100.1", "198.51.100.1"},
		{"127.0.0.1:4000", "", "127.0.0.1"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remote
		if c.realIP != "" {
			req.Header.Set("X-Real-IP", c.realIP)
		}
		if got := clientIP(req); got != c.want {
			t.Errorf("clientIP(%s, X-Real-IP %q) = %s, want %s", c.remote, c.realIP, got, c.want)
		}
	}
}

func TestKeyCache(t *testing.T) {
	keys := defaultKeys()
	a := newAuth(keys, time.Minute, 600)
	now := time.Now()
	a.cache.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if _, err := a.lookup(goodKey); err != nil {
			t.Fatalf("lookup: %v", err)
		}
	}
	if keys.lookups != 1 {
		t.Errorf("store hit %d times within the TTL, want 1", keys.lookups)
	}

	now = now.Add(2 * time.Minute)
	if _, err := a.lookup(goodKey); err != nil {
		t.Fatalf("lookup after TTL: %v", err)
	}
	if keys.lookups != 2 {
		t.Errorf("store hit %d times after the TTL, want 2", keys.lookups)
	}
}
