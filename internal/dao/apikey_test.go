package dao

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func openUpgradedTestDB(t *testing.T) *PGDB {
	t.Helper()
	pg := openTestDB(t)
	if _, _, err := pg.Upgrade(); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	return pg
}

func TestApiKeyLifecycle(t *testing.T) {
	pg := openUpgradedTestDB(t)

	plain, k, err := pg.ApiKey.Create("chess-app", 600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(plain, keyPrefix) || !strings.HasPrefix(plain[len(keyPrefix):], k.Prefix) {
		t.Errorf("key %q does not match stored prefix %q", plain, k.Prefix)
	}
	if k.KeyHash != HashKey(plain) || k.KeyHash == plain {
		t.Error("only the hash of the key should be stored")
	}
	if k.LastUsedAt != nil {
		t.Error("a new key should not be marked used")
	}

	got, err := pg.ApiKey.ByHash(HashKey(plain))
	if err != nil {
		t.Fatalf("by hash: %v", err)
	}
	if got.Id != k.Id || got.Name != "chess-app" || got.RatePerMin != 600 {
		t.Errorf("by hash returned %+v", got)
	}
	if got.LastUsedAt == nil {
		t.Error("lookup should set last_used_at")
	}

	if _, err := pg.ApiKey.ByHash(HashKey("mfide_nope")); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown key: got %v, want sql.ErrNoRows", err)
	}

	if err := pg.ApiKey.Revoke(k.Id); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := pg.ApiKey.ByHash(HashKey(plain)); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("revoked key: got %v, want sql.ErrNoRows", err)
	}
	if err := pg.ApiKey.Revoke(k.Id); err == nil {
		t.Error("revoking an already revoked key should fail")
	}
	if err := pg.ApiKey.Revoke(9999); err == nil {
		t.Error("revoking an unknown id should fail")
	}
}

func TestApiKeyCreateAndList(t *testing.T) {
	pg := openUpgradedTestDB(t)

	if _, _, err := pg.ApiKey.Create("", 600); err == nil {
		t.Error("expected an error for an empty name")
	}
	if _, _, err := pg.ApiKey.Create("x", 0); err == nil {
		t.Error("expected an error for a zero rate")
	}

	a, _, _ := pg.ApiKey.Create("a", 100)
	b, _, _ := pg.ApiKey.Create("b", 100)
	if a == b {
		t.Fatal("two keys should differ")
	}
	keys, err := pg.ApiKey.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 2 || keys[0].Name != "a" || keys[1].Name != "b" {
		t.Errorf("list = %+v", keys)
	}
}
