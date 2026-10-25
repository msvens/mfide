package server

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/msvens/mfide/internal/dao"
	"golang.org/x/time/rate"
)

const apiKeyHeader = "X-API-Key"

// keyStore is the part of dao.ApiKeyDAO auth needs; tests fake it.
type keyStore interface {
	ByHash(hash string) (*dao.ApiKey, error)
}

type auth struct {
	keys         keyStore
	cache        *keyCache
	keyLimits    *limiters
	ipLimits     *limiters
	ipRatePerMin int
}

func newAuth(keys keyStore, cacheTTL time.Duration, ipRatePerMin int) *auth {
	return &auth{
		keys:         keys,
		cache:        &keyCache{ttl: cacheTTL, m: map[string]cachedKey{}, now: time.Now},
		keyLimits:    newLimiters(),
		ipLimits:     newLimiters(),
		ipRatePerMin: ipRatePerMin,
	}
}

type ctxKey struct{}

// apiKeyFrom returns the key a keyed handler was called with.
func apiKeyFrom(r *http.Request) *dao.ApiKey {
	k, _ := r.Context().Value(ctxKey{}).(*dao.ApiKey)
	return k
}

// keyed wraps a handler that requires a valid API key. Requests without one are
// limited per client IP, so hammering with bad keys is cheap to reject; requests
// with one are limited per key.
func (s *mserver) keyed(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		status, key := s.serveKeyed(h, w, r)
		s.l.Infow("request", requestFields(r, status, start, key)...)
	}
}

// serveKeyed answers a keyed request and returns the status sent and the key's
// name ("-" without a valid key).
func (s *mserver) serveKeyed(h handler, w http.ResponseWriter, r *http.Request) (int, string) {
	k, err := s.auth.lookup(r.Header.Get(apiKeyHeader))
	if err != nil {
		if !errors.Is(err, errInvalidKey) {
			return s.response(r, nil, err, w), "-"
		}
		if d := reserve(s.auth.ipLimits.get(clientIP(r), s.auth.ipRatePerMin)); d > 0 {
			return s.tooManyRequests(r, d, w), "-"
		}
		return s.response(r, nil, UnauthorizedError("missing or invalid API key"), w), "-"
	}
	if d := reserve(s.auth.keyLimits.get(strconv.Itoa(k.Id), k.RatePerMin)); d > 0 {
		return s.tooManyRequests(r, d, w), k.Name
	}
	data, err := h(r.WithContext(context.WithValue(r.Context(), ctxKey{}, k)))
	return s.response(r, data, err, w), k.Name
}

func (s *mserver) tooManyRequests(r *http.Request, wait time.Duration, w http.ResponseWriter) int {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	return s.response(r, nil, TooManyRequestsError("rate limit exceeded"), w)
}

var errInvalidKey = errors.New("invalid API key")

// lookup resolves a plaintext key through the cache. Unknown, revoked and missing
// keys give errInvalidKey; anything else is a real failure.
func (a *auth) lookup(plain string) (*dao.ApiKey, error) {
	if plain == "" {
		return nil, errInvalidKey
	}
	hash := dao.HashKey(plain)
	if k := a.cache.get(hash); k != nil {
		return k, nil
	}
	k, err := a.keys.ByHash(hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, errInvalidKey
	case err != nil:
		return nil, err
	}
	a.cache.put(hash, k)
	return k, nil
}

// keyCache holds valid keys only, so random bad keys can't grow it.
type keyCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]cachedKey
	now func() time.Time
}

type cachedKey struct {
	key     *dao.ApiKey
	expires time.Time
}

func (c *keyCache) get(hash string) *dao.ApiKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[hash]; ok && c.now().Before(e.expires) {
		return e.key
	}
	delete(c.m, hash)
	return nil
}

func (c *keyCache) put(hash string, k *dao.ApiKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[hash] = cachedKey{key: k, expires: c.now().Add(c.ttl)}
}

// limiters keeps a token bucket per id (key or IP). Entries idle for idleAfter are
// dropped on the next sweep, which keeps the per-IP map bounded.
type limiters struct {
	mu        sync.Mutex
	m         map[string]*limiterEntry
	lastSweep time.Time
}

type limiterEntry struct {
	l    *rate.Limiter
	seen time.Time
}

const (
	sweepEvery = 5 * time.Minute
	idleAfter  = 10 * time.Minute
)

func newLimiters() *limiters {
	return &limiters{m: map[string]*limiterEntry{}, lastSweep: time.Now()}
}

// get returns the limiter for id. Burst is a tenth of the per-minute rate (at
// least 10), so short spikes pass but a runaway loop is cut off within seconds.
func (ls *limiters) get(id string, perMin int) *rate.Limiter {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	now := time.Now()
	if now.Sub(ls.lastSweep) > sweepEvery {
		for k, e := range ls.m {
			if now.Sub(e.seen) > idleAfter {
				delete(ls.m, k)
			}
		}
		ls.lastSweep = now
	}
	e, ok := ls.m[id]
	if !ok {
		e = &limiterEntry{l: rate.NewLimiter(rate.Limit(float64(perMin)/60), max(10, perMin/10))}
		ls.m[id] = e
	}
	e.seen = now
	return e.l
}

// reserve takes a token, returning 0 if the request may proceed or how long to
// wait if not (in which case nothing is consumed).
func reserve(l *rate.Limiter) time.Duration {
	res := l.Reserve()
	if d := res.Delay(); d > 0 {
		res.Cancel()
		return d
	}
	return 0
}

// clientIP trusts X-Real-IP only from a loopback peer, i.e. our own nginx; from
// anyone else it could be forged.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}
	return host
}
