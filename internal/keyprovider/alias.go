package keyprovider

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// NewAlias builds the label a key carries in the provider's UI. LiteLLM
// requires aliases to be unique across all live keys, and rotation
// deliberately creates the replacement while the old key is still live, so an
// identifier alone would collide on every regeneration.
//
// The subject leads because it is the one identifier that never changes; the
// username follows so a human reading LiteLLM's UI can tell whose key it is.
// An email address is deliberately not used: the IdP can reassign it, which
// would leave the gateway labelling a key with someone else's address.
//
// The suffix is random rather than a timestamp: two rotations can land in the
// same millisecond, so a clock-derived suffix is not actually unique.
//
// The alias is a label, not a binding — ownership is KeyRequest.User. The
// limit sync builds a new alias when the username changes, so the label
// follows the person.
func NewAlias(sub, username string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Falling back to a bare identifier would 400 on the next rotation, so
		// fail here rather than issue a key that cannot be replaced.
		return "", fmt.Errorf("generate key alias: %w", err)
	}

	label := sub
	if username = sanitiseAlias(username); username != "" {
		label += "-" + username
	}
	return label + "-" + hex.EncodeToString(b), nil
}

// sanitiseAlias reduces a username to characters that are safe in an alias.
// Names arrive from the IdP and can carry spaces or punctuation, which would
// make the alias awkward to read and to quote in the gateway's own tooling.
func sanitiseAlias(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		case r == ' ' || r == '.' || r == '@':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
