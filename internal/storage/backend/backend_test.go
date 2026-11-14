package backend_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/storage/backend"
	"github.com/enesoglu/receipt-tracker/internal/storage/local"
)

func TestNew_LocalSelectsConfiguredDirectory(t *testing.T) {
	store, err := backend.New(t.Context(), config.Storage{Backend: config.StorageLocal, LocalDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Store.(*local.Store); !ok {
		t.Fatalf("selected %T", store.Store)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNew_DriveDoesNotFallBackToLocal(t *testing.T) {
	store, err := backend.New(t.Context(), config.Storage{Backend: config.StorageDrive, LocalDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, putErr := store.Put(t.Context(), "image.png", strings.NewReader("x"), 1, "image/png")
	_, openErr := store.Open(t.Context(), "image.png")
	deleteErr := store.Delete(t.Context(), "image.png")
	for _, err := range []error{putErr, openErr, deleteErr} {
		var classified *apperr.Error
		if !errors.Is(err, apperr.Unavailable) || !errors.As(err, &classified) || classified.Code != "storage_not_connected" {
			t.Fatalf("unconnected Drive: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNew_UnknownBackendIsInvalid(t *testing.T) {
	if _, err := backend.New(t.Context(), config.Storage{Backend: "unknown"}, nil); !errors.Is(err, apperr.Invalid) {
		t.Fatalf("unknown backend: %v", err)
	}
}
