// Package local stores backup streams within a confined local directory.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/backup"
)

// Store confines filesystem operations to its open directory.
type Store struct{ root *os.Root }

// New opens or creates a private backup directory.
func New(ctx context.Context, directory string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create backup directory: %w", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open backup directory: %w", err)
	}
	return &Store{root: root}, nil
}

// Close releases the directory handle.
func (s *Store) Close() error { return s.root.Close() }

// Put streams an object and removes incomplete writes on any failure.
func (s *Store) Put(ctx context.Context, name string, source io.Reader) (result error) {
	if err := backup.ValidateName(name); err != nil {
		return err
	}
	if source == nil {
		return apperr.New(apperr.Invalid, "missing backup stream")
	}
	file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	defer func() {
		result = errors.Join(result, file.Close())
		if result != nil {
			result = errors.Join(result, s.root.Remove(name))
		}
	}()
	if _, err := io.Copy(file, &reader{ctx: ctx, source: source}); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	return file.Sync()
}

// List returns complete dump names in ascending order with their sizes.
func (s *Store) List(ctx context.Context) ([]backup.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := s.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close() //nolint:errcheck // Read-only directory handle.
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	objects := make([]backup.Object, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || backup.ValidateName(entry.Name()) != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat backup: %w", err)
		}
		objects = append(objects, backup.Object{Name: entry.Name(), Size: info.Size()})
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Name < objects[j].Name })
	return objects, nil
}

// Open returns a dump stream or a classified missing-object error.
func (s *Store) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := backup.ValidateName(name); err != nil {
		return nil, err
	}
	file, err := s.root.Open(name)
	return file, classify(err)
}

// Delete removes a dump within the configured directory.
func (s *Store) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := backup.ValidateName(name); err != nil {
		return err
	}
	return classify(s.root.Remove(name))
}

func classify(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return apperr.New(apperr.NotFound, "backup not found")
	}
	return err
}

type reader struct {
	ctx    context.Context
	source io.Reader
}

func (r *reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}
