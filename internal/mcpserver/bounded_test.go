package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoundedAcceptsExactlyTheByteLimit(t *testing.T) {
	empty, err := bounded("")
	if err != nil {
		t.Fatal(err)
	}
	encodedEmpty, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	// A plain ASCII string adds exactly its length to the encoded wrapper.
	filler := strings.Repeat("x", maxResultBytes-len(encodedEmpty))
	result, err := bounded(filler)
	if err != nil {
		t.Fatalf("exactly-sized result rejected: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != maxResultBytes {
		t.Fatalf("encoded size = %d, want %d", len(encoded), maxResultBytes)
	}
	if _, err := bounded(filler + "x"); err == nil {
		t.Fatal("oversized result accepted")
	}
}
