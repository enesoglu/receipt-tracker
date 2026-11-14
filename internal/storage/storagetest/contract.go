// Package storagetest provides the contract suite shared by storage adapters.
package storagetest

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/storage"
)

// Run verifies each Store against the same stream and lifecycle contract.
// Each subtest receives an isolated adapter and may use it concurrently.
func Run(t *testing.T, factory func(*testing.T) storage.Store) {
	t.Helper()
	t.Run("round trip", func(t *testing.T) {
		store := factory(t)
		for _, data := range [][]byte{nil, []byte("synthetic receipt image")} {
			ref, err := store.Put(t.Context(), key(t), bytes.NewReader(data), int64(len(data)), "image/png")
			if err != nil {
				t.Fatal(err)
			}
			reader, err := store.Open(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(reader)
			closeErr := reader.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("read/close: %v / %v", err, closeErr)
			}
			if diff := cmp.Diff(string(data), string(got)); diff != "" {
				t.Fatal(diff)
			}
		}
	})
	t.Run("missing objects", func(t *testing.T) {
		store := factory(t)
		ref := key(t)
		if reader, err := store.Open(t.Context(), ref); !errors.Is(err, apperr.NotFound) {
			if reader != nil {
				_ = reader.Close()
			}
			t.Fatalf("open missing object: %v", err)
		}
		if err := store.Delete(t.Context(), ref); !errors.Is(err, apperr.NotFound) {
			t.Fatalf("delete missing object: %v", err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		store := factory(t)
		ref, err := store.Put(t.Context(), key(t), bytes.NewBufferString("image"), 5, "image/png")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Delete(t.Context(), ref); err != nil {
			t.Fatal(err)
		}
		if reader, err := store.Open(t.Context(), ref); !errors.Is(err, apperr.NotFound) {
			if reader != nil {
				_ = reader.Close()
			}
			t.Fatalf("open deleted object: %v", err)
		}
	})
	t.Run("large bounded stream", func(t *testing.T) {
		store := factory(t)
		const size = 32 << 20
		source := &boundedReader{remaining: size, maxRead: 1 << 20}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		ref, err := store.Put(t.Context(), key(t), source, size, "image/png")
		if err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
			t.Fatalf("upload allocated %d bytes for a %d-byte stream", allocated, size)
		}
		reader, err := store.Open(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		count, err := io.Copy(hash, reader)
		closeErr := reader.Close()
		if err != nil || closeErr != nil || count != size {
			t.Fatalf("read %d: %v / %v", count, err, closeErr)
		}
		expected := sha256.New()
		if _, err := io.Copy(expected, &boundedReader{remaining: size, maxRead: 1 << 20}); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(expected.Sum(nil), hash.Sum(nil)); diff != "" {
			t.Fatal(diff)
		}
	})
	t.Run("concurrent puts", func(t *testing.T) {
		store := factory(t)
		for i := range 12 {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				data := fmt.Sprintf("image %d", i)
				ref, err := store.Put(t.Context(), key(t), bytes.NewBufferString(data), int64(len(data)), "image/png")
				if err != nil {
					t.Fatal(err)
				}
				reader, err := store.Open(t.Context(), ref)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(reader)
				closeErr := reader.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("read/close: %v / %v", err, closeErr)
				}
				if diff := cmp.Diff(data, string(got)); diff != "" {
					t.Fatal(diff)
				}
			})
		}
	})
}

func key(t *testing.T) string {
	t.Helper()
	value, err := storage.Key(uuid.New(), uuid.New(), uuid.New(), "png")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type boundedReader struct {
	remaining int64
	maxRead   int
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if len(p) > r.maxRead {
		return 0, fmt.Errorf("unbounded read buffer: %d", len(p))
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), r.remaining))
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.remaining -= int64(n)
	return n, nil
}
