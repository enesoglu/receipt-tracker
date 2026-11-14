package local_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/storage"
	"github.com/enesoglu/receipt-tracker/internal/storage/local"
	"github.com/enesoglu/receipt-tracker/internal/storage/storagetest"
)

func newStore(t *testing.T, root string) *local.Store {
	t.Helper()
	store, err := local.New(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestLocal_StorageContract(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Store { return newStore(t, t.TempDir()) })
}

func TestLocal_RefusesTraversalForEveryOperation(t *testing.T) {
	store := newStore(t, t.TempDir())
	for _, key := range []string{"", ".", "..", "../escape", "a/../../escape", "/absolute", `C:\absolute`, `a\..\escape`, "a//b", "a/./b", "%2e%2e/escape", "a%2fb", "a%5Cb", "a%252fb", "a/b.", "a/b ", "a/NUL.png", "CON", "a/x:stream", "a/\x00b"} {
		t.Run(key, func(t *testing.T) {
			if _, err := store.Put(t.Context(), key, strings.NewReader("x"), 1, "image/png"); !errors.Is(err, apperr.Invalid) {
				t.Fatalf("put: %v", err)
			}
			if reader, err := store.Open(t.Context(), key); !errors.Is(err, apperr.Invalid) {
				if reader != nil {
					_ = reader.Close()
				}
				t.Fatalf("open: %v", err)
			}
			if err := store.Delete(t.Context(), key); !errors.Is(err, apperr.Invalid) {
				t.Fatalf("delete: %v", err)
			}
		})
	}
}

func TestLocal_SymlinkCannotEscapeRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	store := newStore(t, root)
	if err := os.WriteFile(filepath.Join(outside, "image.png"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.Put(t.Context(), "escape/image.png", strings.NewReader("attack"), 6, "image/png"); err == nil {
		t.Fatal("put escaped root")
	}
	if reader, err := store.Open(t.Context(), "escape/image.png"); err == nil {
		_ = reader.Close()
		t.Fatal("open escaped root")
	}
	if err := store.Delete(t.Context(), "escape/image.png"); err == nil {
		t.Fatal("delete escaped root")
	}
	outsideRoot, err := os.OpenRoot(outside)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := outsideRoot.Close(); err != nil {
			t.Error(err)
		}
	}()
	got, err := outsideRoot.ReadFile("image.png")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("outside", string(got)); diff != "" {
		t.Fatal(diff)
	}
}

func TestLocal_FailedWritePreservesPreviousObjectAndRemovesTemporary(t *testing.T) {
	root := t.TempDir()
	store := newStore(t, root)
	const key = "user/receipt/image.png"
	ref, err := store.Put(t.Context(), key, strings.NewReader("previous"), 8, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]struct {
		reader io.Reader
		size   int64
	}{
		"short":    {strings.NewReader("x"), 2},
		"long":     {strings.NewReader("xx"), 1},
		"failed":   {&failedReader{}, 1},
		"negative": {strings.NewReader("x"), -1},
		"nil":      {nil, 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Put(t.Context(), key, input.reader, input.size, "image/png"); err == nil {
				t.Fatal("write succeeded")
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
			if diff := cmp.Diff("previous", string(got)); diff != "" {
				t.Fatal(diff)
			}
			files, err := os.ReadDir(filepath.Join(root, "user", "receipt"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 || files[0].Name() != "image.png" {
				t.Fatalf("temporary files remain: %v", files)
			}
		})
	}
	// A successful replacement also publishes the new complete value.
	if _, err := store.Put(t.Context(), key, strings.NewReader("new"), 3, "image/png"); err != nil {
		t.Fatal(err)
	}
}

func TestLocal_CancellationPreventsPublication(t *testing.T) {
	root := t.TempDir()
	store := newStore(t, root)
	ctx, cancel := context.WithCancel(t.Context())
	reader := &cancelReader{cancel: cancel}
	if _, err := store.Put(ctx, "image.png", reader, 10, "image/png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "image.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled image published: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary file cleanup: %v, %v", entries, err)
	}
	if _, err := store.Put(ctx, "image.png", bytes.NewReader(nil), 0, "image/png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled put: %v", err)
	}
	if _, err := store.Open(ctx, "image.png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open: %v", err)
	}
	if err := store.Delete(ctx, "image.png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delete: %v", err)
	}
}

func TestLocal_OpenReaderHonorsCancellation(t *testing.T) {
	store := newStore(t, t.TempDir())
	ref, err := store.Put(t.Context(), "image.png", strings.NewReader("image"), 5, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reader, err := store.Open(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, readErr := reader.Read(make([]byte, 1))
	closeErr := reader.Close()
	if !errors.Is(readErr, context.Canceled) || closeErr != nil {
		t.Fatalf("read/close: %v / %v", readErr, closeErr)
	}
}

type failedReader struct{}

func (*failedReader) Read([]byte) (int, error) { return 0, errors.New("synthetic stream failure") }

type cancelReader struct{ cancel context.CancelFunc }

func (r *cancelReader) Read(p []byte) (int, error) { r.cancel(); p[0] = 'x'; return 1, nil }

func TestLocal_IncompleteWriteIsInvisible(t *testing.T) {
	store := newStore(t, t.TempDir())
	const key = "image.png"
	input := &gatedReader{started: make(chan struct{}), proceed: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := store.Put(t.Context(), key, input, 5, "image/png")
		result <- err
	}()
	<-input.started
	reader, err := store.Open(t.Context(), key)
	if reader != nil {
		_ = reader.Close()
	}
	close(input.proceed)
	putErr := <-result
	if !errors.Is(err, apperr.NotFound) {
		t.Fatalf("incomplete image visible: %v", err)
	}
	if putErr != nil {
		t.Fatal(putErr)
	}
	reader, err = store.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close: %v / %v", readErr, closeErr)
	}
	if diff := cmp.Diff("image", string(got)); diff != "" {
		t.Fatal(diff)
	}
}

type gatedReader struct {
	started chan struct{}
	proceed chan struct{}
	read    bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	close(r.started)
	<-r.proceed
	return copy(p, "image"), nil
}
