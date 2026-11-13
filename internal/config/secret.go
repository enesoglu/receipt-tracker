package config

import (
	"crypto/subtle"
	"log/slog"
)

// redacted replaces a secret wherever it would be printed.
const redacted = "[redacted]"

// Secret is a value that must not appear in logs, errors or serialised
// output: a password, a key, a token or a URL that contains one. Formatting
// a Secret, or a struct that holds one, prints "[redacted]". Code that
// needs the value calls [Secret.Reveal], which makes every use easy to find.
type Secret string

// Reveal returns the secret value.
func (s Secret) Reveal() string { return string(s) }

// String hides the value from fmt verbs such as %s and %v.
func (Secret) String() string { return redacted }

// GoString hides the value from the %#v verb.
func (Secret) GoString() string { return redacted }

// LogValue hides the value from slog.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText hides the value from encoders such as encoding/json.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Key is a 256-bit secret key. Like [Secret] it never prints its value.
type Key struct {
	bytes [encryptionKeyBytes]byte
}

// newKey copies raw, which must be exactly 32 bytes long.
func newKey(raw []byte) Key {
	var key Key
	copy(key.bytes[:], raw)
	return key
}

// Bytes returns a copy of the key material.
func (k Key) Bytes() []byte { return append([]byte(nil), k.bytes[:]...) }

// Equal reports whether both keys hold the same material, in constant time.
func (k Key) Equal(other Key) bool {
	return subtle.ConstantTimeCompare(k.bytes[:], other.bytes[:]) == 1
}

// String hides the value from fmt verbs such as %s and %v.
func (Key) String() string { return redacted }

// GoString hides the value from the %#v verb.
func (Key) GoString() string { return redacted }

// LogValue hides the value from slog.
func (Key) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText hides the value from encoders such as encoding/json.
func (Key) MarshalText() ([]byte, error) { return []byte(redacted), nil }
