// Package local stores receipt images atomically beneath a private directory.
package local

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/storage"
)

// Store confines every operation, including temporary files, to an os.Root.
// The configured directory must be private to the application.
type Store struct{ root *os.Root }

// New creates and opens the configured storage directory.
func New(ctx context.Context, directory string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open local storage: %w", err)
	}
	if directory == "" {
		return nil, apperr.New(apperr.Invalid, "local storage directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, failure("create local storage", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, failure("open local storage", err)
	}
	return &Store{root: root}, nil
}

// Close releases the directory handle after all operations have completed.
func (s *Store) Close() error { return s.root.Close() }

// Put writes a bounded stream to a private temporary file, then renames it.
// A failed or canceled write leaves any previous object intact.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, _ string) (string, error) {
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("put local image: %w", err)
	}
	if size < 0 || r == nil {
		return "", apperr.New(apperr.Invalid, "image stream and nonnegative size are required")
	}
	parent := path.Dir(key)
	if err := s.root.MkdirAll(parent, 0o700); err != nil {
		return "", failure("create image directory", err)
	}
	temporary := path.Join(parent, ".upload-"+rand.Text())
	file, err := s.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", failure("create image temporary file", err)
	}
	defer func() { _ = file.Close(); _ = s.root.Remove(temporary) }()
	reader := &contextReader{ctx: ctx, reader: r}
	if _, err := io.CopyN(file, reader, size); err != nil {
		return "", failure("write image stream", err)
	}
	if _, err := io.CopyN(io.Discard, reader, 1); !errors.Is(err, io.EOF) {
		if err != nil {
			return "", failure("finish image stream", err)
		}
		return "", apperr.New(apperr.Invalid, "image stream exceeds declared size")
	}
	if err := file.Sync(); err != nil {
		return "", failure("sync image", err)
	}
	if err := file.Close(); err != nil {
		return "", failure("close image", err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("put local image: %w", err)
	}
	if err := s.root.Rename(temporary, key); err != nil {
		return "", failure("publish image", err)
	}
	return key, nil
}

// Open returns a reader owned by the caller, with cancellation checked per read.
func (s *Store) Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	if err := storage.ValidateKey(ref); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open local image: %w", err)
	}
	file, err := s.root.Open(ref)
	if err != nil {
		return nil, failure("open local image", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, failure("inspect local image", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, apperr.New(apperr.Invalid, "image reference is not a regular file")
	}
	return &contextReadCloser{contextReader: contextReader{ctx: ctx, reader: file}, closer: file}, nil
}

// Delete removes an object and classifies a missing reference as NotFound.
func (s *Store) Delete(ctx context.Context, ref string) error {
	if err := storage.ValidateKey(ref); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete local image: %w", err)
	}
	info, err := s.root.Lstat(ref)
	if err != nil {
		return failure("inspect local image", err)
	}
	if !info.Mode().IsRegular() {
		return apperr.New(apperr.Invalid, "image reference is not a regular file")
	}
	if err := s.root.Remove(ref); err != nil {
		return failure("delete local image", err)
	}
	return nil
}

func failure(operation string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return apperr.Wrap(apperr.NotFound, "stored image not found", err)
	}
	return apperr.Wrap(apperr.Unavailable, "local image storage is unavailable", fmt.Errorf("%s: %w", operation, err))
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type contextReadCloser struct {
	contextReader
	closer io.Closer
}

func (r *contextReadCloser) Close() error { return r.closer.Close() }
