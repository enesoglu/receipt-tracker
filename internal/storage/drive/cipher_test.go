package drive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTokenCipher_AuthenticatedAndKeyed(t *testing.T) {
	first, err := newTokenCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTokenCipher(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	token := "synthetic-secret-refresh-token"
	encrypted := first.encrypt(token)
	if strings.Contains(encrypted, token) || encrypted == first.encrypt(token) {
		t.Fatal("plaintext leaked or nonce reused")
	}
	decoded, err := first.decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(token, decoded); diff != "" {
		t.Fatal(diff)
	}
	for _, value := range []string{"", first.keyID, encrypted[:len(encrypted)-5], encrypted[:len(encrypted)-5] + "AAAAA", "v2:" + encrypted} {
		if _, err := first.decrypt(value); err == nil {
			t.Errorf("accepted corrupt ciphertext: %q", value)
		}
	}
	if _, err := second.decrypt(encrypted); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err := newTokenCipher([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}
