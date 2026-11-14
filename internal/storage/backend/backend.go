// Package backend selects the configured receipt image store at startup.
package backend

import (
	"context"
	"fmt"
	"io"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/storage"
	"github.com/enesoglu/receipt-tracker/internal/storage/local"
)

// Backend owns the selected adapter and its process-lifetime resources.
type Backend struct {
	storage.Store
	closer io.Closer
}

// New selects the configured adapter. Drive remains unavailable until an owner
// connects it; selecting Drive never silently falls back to local storage.
func New(ctx context.Context, settings config.Storage) (*Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("select image storage: %w", err)
	}
	switch settings.Backend {
	case config.StorageLocal:
		store, err := local.New(ctx, settings.LocalDir)
		if err != nil {
			return nil, err
		}
		return &Backend{Store: store, closer: store}, nil
	case config.StorageDrive:
		return &Backend{Store: disconnected{}}, nil
	default:
		return nil, apperr.New(apperr.Invalid, "unsupported image storage backend")
	}
}

// Close releases the adapter's resources when the server stops.
func (b *Backend) Close() error {
	if b.closer != nil {
		return b.closer.Close()
	}
	return nil
}

// disconnected keeps Drive selection explicit while its connection flow is
// delivered in the next storage PR. No operation can write to a fallback store.
type disconnected struct{}

func (disconnected) Put(_ context.Context, _ string, _ io.Reader, _ int64, _ string) (string, error) {
	return "", notConnected()
}

func (disconnected) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, notConnected()
}
func (disconnected) Delete(_ context.Context, _ string) error { return notConnected() }
func notConnected() error {
	return apperr.New(apperr.Unavailable, "connect Google Drive to use image storage").WithCode("storage_not_connected")
}
