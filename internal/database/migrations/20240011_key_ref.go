package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// KeyRefName is this migration's name. Store.RunMigrations vacuums the
// database after it runs, so the plaintext keys it replaces do not linger in
// free pages.
const KeyRefName = "20240011"

// Store each key's hash instead of the key itself.
//
// litellm_key held every live secret in plaintext, so anyone who could read
// the database file or a backup had a working key for every user. LiteLLM
// identifies a key by the SHA-256 of it and accepts that hash wherever it
// takes the key, so the hash is all the portal needs. The column is renamed to
// key_ref to match: it holds the provider's ref, not a key.
//
// The hash is computed here rather than with the litellm package, so the
// migration keeps doing what it did when it was written. A value that already
// is a SHA-256 in hex is left alone, in case one was stored that way.
//
// There is no way back: a hash cannot be turned into a key again, and the
// previous release would hash the hash before looking up usage. Down fails
// instead of leaving a database the older code misreads.
func init() {
	Migrations.MustRegister(inTx(func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE api_keys RENAME COLUMN litellm_key TO key_ref`); err != nil {
			return fmt.Errorf("rename litellm_key: %w", err)
		}

		type row struct {
			ID  int64  `bun:"id"`
			Ref string `bun:"key_ref"`
		}
		var rows []row
		if err := tx.NewRaw(`SELECT id, key_ref FROM api_keys`).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("read keys: %w", err)
		}
		for _, r := range rows {
			if isSHA256Hex(r.Ref) {
				continue
			}
			sum := sha256.Sum256([]byte(r.Ref))
			if _, err := tx.ExecContext(ctx,
				`UPDATE api_keys SET key_ref = ? WHERE id = ?`,
				hex.EncodeToString(sum[:]), r.ID); err != nil {
				return fmt.Errorf("hash key %d: %w", r.ID, err)
			}
		}
		return nil
	}), inTx(func(ctx context.Context, tx bun.Tx) error {
		return errors.New("api_keys.key_ref holds hashes, which cannot be turned back into keys")
	}))
}

// isSHA256Hex reports whether s looks like a hex-encoded SHA-256, the form
// LiteLLM uses for a key's hash.
func isSHA256Hex(s string) bool {
	if len(s) != 2*sha256.Size {
		return false
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}
