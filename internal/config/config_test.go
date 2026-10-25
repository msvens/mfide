package config

import (
	"testing"
	"time"
)

// config_example.yaml documents every section; keep it in sync with the accessors.
func TestConfigExample(t *testing.T) {
	if err := readConfig("config_example"); err != nil {
		t.Fatalf("read config_example.yaml: %v", err)
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"server.prefix", ServerPrefix(), "/api"},
		{"server.addr", ServerAddr(), "localhost:8051"},
		{"service.root", ServiceRoot(), ".server"},
		{"api.defaultRatePerMin", ApiDefaultRatePerMin(), 1000},
		{"api.keyCacheTTL", ApiKeyCacheTTL(), 60 * time.Second},
		{"api.ipRatePerMin", ApiIPRatePerMin(), 60},
		{"fide.baseUrl", FideBaseURL(), "https://ratings.fide.com/download"},
		{"fide.fetchDelay", FideFetchDelay(), 3 * time.Second},
		{"sync.enabled", SyncEnabled(), true},
		{"sync.at", SyncAt(), "03:00"},
		{"retention.currentFiles", RetentionCurrentFiles(), 2},
		{"retention.removedPlayersDays", RetentionRemovedPlayersDays(), 90},
		{"retention.historyMonths", RetentionHistoryMonths(), 60},
		{"log.format", LogFormat(), "console"},
		{"log.level", LogLevel(), "info"},
		{"db.host", DbHost(), "localhost"},
		{"db.port", DbPort(), 5432},
		{"db.user", DbUser(), "user"},
		{"db.password", DbPassword(), "password"},
		{"db.name", DbName(), "name"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}
