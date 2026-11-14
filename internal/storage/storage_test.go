package storage_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/storage"
)

func TestKey_UsesOnlyIdentifiersAndTrustedExtension(t *testing.T) {
	user := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	receipt := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	image := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	for _, extension := range []string{"jpg", "jpeg", "png", "heic", "heif"} {
		got, err := storage.Key(user, receipt, image, extension)
		if err != nil {
			t.Fatal(err)
		}
		want := user.String() + "/" + receipt.String() + "/" + image.String() + "." + extension
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatal(diff)
		}
		if err := storage.ValidateKey(got); err != nil {
			t.Fatal(err)
		}
	}
	for _, extension := range []string{"", ".png", "../png", "photo.png", "PNG", "png/escape"} {
		if _, err := storage.Key(user, receipt, image, extension); !errors.Is(err, apperr.Invalid) {
			t.Fatalf("extension %q: %v", extension, err)
		}
	}
	for _, ids := range [][3]uuid.UUID{{uuid.Nil, receipt, image}, {user, uuid.Nil, image}, {user, receipt, uuid.Nil}} {
		if _, err := storage.Key(ids[0], ids[1], ids[2], "png"); !errors.Is(err, apperr.Invalid) {
			t.Fatalf("empty identifier: %v", err)
		}
	}
}
