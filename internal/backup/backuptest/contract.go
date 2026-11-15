// Package backuptest supplies the contract shared by backup adapters.
package backuptest

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/backup"
)

// Run checks ordering, confinement, round trips and bounded stream allocation.
func Run(t *testing.T, newDestination func(*testing.T) backup.Destination) {
	t.Helper()
	t.Run("round trip and confinement", func(t *testing.T) {
		destination := newDestination(t)
		names := []string{"20261115T020000.000000000Z.dump", "20261114T020000.000000000Z.dump"}
		for _, name := range names {
			if err := destination.Put(t.Context(), name, strings.NewReader("synthetic dump")); err != nil {
				t.Fatal(err)
			}
		}
		objects, err := destination.List(t.Context())
		if err != nil || len(objects) != 2 || objects[0].Name != names[1] || objects[1].Name != names[0] || objects[0].Size != 14 {
			t.Fatalf("listing: %+v %v", objects, err)
		}
		stream, err := destination.Open(t.Context(), names[0])
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		closeErr := stream.Close()
		if err != nil || closeErr != nil || string(data) != "synthetic dump" {
			t.Fatalf("round trip: %q %v %v", data, err, closeErr)
		}
		if err := destination.Delete(t.Context(), names[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := destination.Open(t.Context(), names[0]); !errors.Is(err, apperr.NotFound) {
			t.Fatalf("missing open: %v", err)
		}
		if err := destination.Delete(t.Context(), names[0]); !errors.Is(err, apperr.NotFound) {
			t.Fatalf("missing delete: %v", err)
		}
		for _, bad := range []string{"../escape.dump", "/absolute.dump", "x.dump", `a\b.dump`} {
			if err := destination.Put(t.Context(), bad, strings.NewReader("x")); !errors.Is(err, apperr.Invalid) {
				t.Fatalf("unsafe put %q: %v", bad, err)
			}
			if _, err := destination.Open(t.Context(), bad); !errors.Is(err, apperr.Invalid) {
				t.Fatalf("unsafe open: %v", err)
			}
			if err := destination.Delete(t.Context(), bad); !errors.Is(err, apperr.Invalid) {
				t.Fatalf("unsafe delete: %v", err)
			}
		}
	})
	t.Run("large stream has flat memory", func(t *testing.T) {
		destination := newDestination(t)
		// Initialize discovery and transport before measuring the stream allocation.
		if _, err := destination.List(t.Context()); err != nil {
			t.Fatal(err)
		}
		const size = 32 << 20
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		name := "20261115T030000.000000000Z.dump"
		if err := destination.Put(t.Context(), name, io.LimitReader(zeroReader{}, size)); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		if used := after.TotalAlloc - before.TotalAlloc; used > 16<<20 {
			t.Fatalf("allocated %d bytes for a %d byte stream", used, size)
		}
		stream, err := destination.Open(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		count, err := io.Copy(io.Discard, stream)
		closeErr := stream.Close()
		if err != nil || closeErr != nil || count != size {
			t.Fatalf("stream: %d %v %v", count, err, closeErr)
		}
	})
	t.Run("broken stream is not published", func(t *testing.T) {
		destination := newDestination(t)
		name := "20261115T040000.000000000Z.dump"
		if err := destination.Put(t.Context(), name, io.MultiReader(bytes.NewBufferString("partial"), brokenReader{})); err == nil {
			t.Fatal("broken upload succeeded")
		}
		objects, err := destination.List(t.Context())
		if err != nil || len(objects) != 0 {
			t.Fatalf("partial object: %+v %v", objects, err)
		}
	})
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("synthetic stream failure") }
