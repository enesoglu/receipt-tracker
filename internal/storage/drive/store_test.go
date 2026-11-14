package drive

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/storage"

	"github.com/enesoglu/receipt-tracker/internal/storage/drivetest"
	"github.com/enesoglu/receipt-tracker/internal/storage/storagetest"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	fake := drivetest.New(t)
	api, err := gdrive.NewService(t.Context(), option.WithHTTPClient(fake.Server.Client()), option.WithEndpoint(fake.Server.URL+"/drive/v3/"))
	if err != nil {
		t.Fatal(err)
	}
	return newStore(api, "synthetic-root")
}

func TestDrive_StorageContract(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Store { return testStore(t) })
}

func TestDrive_RejectsTraversalAndBrokenStreams(t *testing.T) {
	store := testStore(t)
	for _, key := range []string{"../image.png", "/image.png", `a\b.png`, "a%2fb.png"} {
		if _, err := store.Put(t.Context(), key, strings.NewReader("x"), 1, "image/png"); !errors.Is(err, apperr.Invalid) {
			t.Fatalf("key %q: %v", key, err)
		}
	}
	for _, test := range []struct {
		data string
		size int64
	}{{"x", 2}, {"xx", 1}, {"x", -1}} {
		if _, err := store.Put(t.Context(), "image.png", strings.NewReader(test.data), test.size, "image/png"); err == nil {
			t.Fatalf("accepted mismatched stream: %+v", test)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Put(ctx, "image.png", strings.NewReader("x"), 1, "image/png"); err == nil {
		t.Fatal("canceled write succeeded")
	}
}

func TestOperationError_DoesNotExposeProviderDetails(t *testing.T) {
	for _, test := range []struct {
		name             string
		input, errorKind error
	}{
		{"canceled", &url.Error{Op: "Get", URL: "https://example.invalid/?token=synthetic-secret", Err: context.Canceled}, context.Canceled},
		{"timeout", &url.Error{Op: "Get", URL: "https://example.invalid/?token=synthetic-secret", Err: context.DeadlineExceeded}, context.DeadlineExceeded},
		{"provider", &googleapi.Error{Code: 503, Message: "synthetic-secret", Body: "synthetic-secret"}, apperr.Unavailable},
		{"missing", &googleapi.Error{Code: 404, Message: "synthetic-secret"}, apperr.NotFound},
		{"classified", fmt.Errorf("synthetic-secret: %w", unavailable("storage_reconnect_needed")), apperr.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := operationError(test.input)
			if !errors.Is(err, test.errorKind) || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "example.invalid") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}
