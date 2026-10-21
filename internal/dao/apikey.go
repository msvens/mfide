package dao

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jmoiron/sqlx"
)

// keyPrefix marks mfide keys so a leaked one is easy to recognize and grep for.
const keyPrefix = "mfide_"

// schemaApiKey stores only the sha256 of each key; the plaintext is shown once at
// creation. prefix holds the first characters after keyPrefix, for listing and logs.
const schemaApiKey = `
CREATE TABLE api_key (
	id           SERIAL PRIMARY KEY,
	name         TEXT NOT NULL,
	prefix       TEXT NOT NULL,
	key_hash     TEXT NOT NULL UNIQUE,
	rate_per_min INT NOT NULL,
	created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	revoked_at   TIMESTAMPTZ,
	last_used_at TIMESTAMPTZ
);
`

type ApiKey struct {
	Id         int        `db:"id"`
	Name       string     `db:"name"`
	Prefix     string     `db:"prefix"`
	KeyHash    string     `db:"key_hash"`
	RatePerMin int        `db:"rate_per_min"`
	CreatedAt  time.Time  `db:"created_at"`
	RevokedAt  *time.Time `db:"revoked_at"`
	LastUsedAt *time.Time `db:"last_used_at"`
}

type ApiKeyDAO interface {
	// Create issues a new key and returns its plaintext, which is not stored.
	Create(name string, ratePerMin int) (string, *ApiKey, error)
	List() ([]ApiKey, error)
	Revoke(id int) error
	// ByHash returns the active key with this hash and marks it used. Unknown and
	// revoked keys give sql.ErrNoRows.
	ByHash(hash string) (*ApiKey, error)
}

type ApiKeyPG struct {
	db *sqlx.DB
}

func NewApiKeyPG(db *sqlx.DB) *ApiKeyPG {
	return &ApiKeyPG{db: db}
}

// HashKey is how keys are stored and looked up.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// newKey returns keyPrefix + 32 random bytes in base62.
func newKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return keyPrefix + new(big.Int).SetBytes(b).Text(62), nil
}

func (dao *ApiKeyPG) Create(name string, ratePerMin int) (string, *ApiKey, error) {
	if name == "" {
		return "", nil, errors.New("key name is required")
	}
	if ratePerMin <= 0 {
		return "", nil, fmt.Errorf("rate per minute must be positive, got %d", ratePerMin)
	}
	plain, err := newKey()
	if err != nil {
		return "", nil, err
	}
	k := ApiKey{}
	err = dao.db.Get(&k, `INSERT INTO api_key (name, prefix, key_hash, rate_per_min)
		VALUES ($1, $2, $3, $4) RETURNING *`,
		name, plain[len(keyPrefix):len(keyPrefix)+8], HashKey(plain), ratePerMin)
	if err != nil {
		return "", nil, err
	}
	return plain, &k, nil
}

func (dao *ApiKeyPG) List() ([]ApiKey, error) {
	keys := []ApiKey{}
	err := dao.db.Select(&keys, "SELECT * FROM api_key ORDER BY id")
	return keys, err
}

func (dao *ApiKeyPG) Revoke(id int) error {
	res, err := dao.db.Exec("UPDATE api_key SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no active key with id %d", id)
	}
	return nil
}

func (dao *ApiKeyPG) ByHash(hash string) (*ApiKey, error) {
	k := ApiKey{}
	err := dao.db.Get(&k, `UPDATE api_key SET last_used_at = now()
		WHERE key_hash = $1 AND revoked_at IS NULL RETURNING *`, hash)
	if err != nil {
		return nil, err
	}
	return &k, nil
}
