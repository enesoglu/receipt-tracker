// Package oauthserver implements the small public-client OAuth server for MCP.
package oauthserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Credentials contain a routing hint followed by 256 random bits. The hint is
// untrusted until the complete hash matches a row scoped to that user. This
// keeps even credential lookups explicitly user-scoped without a global index
// lookup that returns somebody else's credentials. Clients treat the whole
// value as opaque; neither identity nor permission is trusted from its bytes.
func newCredential(userID uuid.UUID) string {
	raw := make([]byte, 48)
	copy(raw, userID[:])
	_, _ = rand.Read(raw[16:])
	return base64.RawURLEncoding.EncodeToString(raw)
}

func credentialOwner(value string) (uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != 48 {
		return uuid.Nil, false
	}
	id, err := uuid.FromBytes(raw[:16])
	return id, err == nil && id != uuid.Nil
}

func digest(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func validVerifier(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)
		if !allowed {
			return false
		}
	}
	return true
}

func validChallenge(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

func redirectURL(value string) (*url.URL, bool) {
	u, err := url.Parse(value)
	if err != nil || len(value) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, false
	}
	allowed := u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname())
	if !allowed {
		return nil, false
	}
	return u, true
}

func loopback(host string) bool { return host == "localhost" || host == "127.0.0.1" || host == "::1" }

func matchingRedirect(registered, requested string) bool {
	a, aOK := redirectURL(registered)
	b, bOK := redirectURL(requested)
	if !aOK || !bOK {
		return false
	}
	// Claude Code also declares loopback redirects without an ephemeral port.
	if loopback(a.Hostname()) && a.Hostname() == b.Hostname() {
		a.Host = a.Hostname()
		b.Host = b.Hostname()
		return a.String() == b.String()
	}
	return registered == requested
}

func normalizeScope(value string) (string, bool) {
	if value == "" {
		return "receipts:read offline_access", true
	}
	read, offline := false, false
	for _, part := range strings.Fields(value) {
		switch part {
		case "receipts:read":
			read = true
		case "offline_access":
			offline = true
		default:
			return "", false
		}
	}
	if !read {
		return "", false
	}
	if offline {
		return "receipts:read offline_access", true
	}
	return "receipts:read", true
}
