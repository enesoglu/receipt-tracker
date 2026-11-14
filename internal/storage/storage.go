// Package storage defines the storage boundary for receipt images.
// Callers check ownership before opening or deleting an opaque reference.
package storage

import (
	"context"
	"io"
	"io/fs"
	"strings"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// Store persists streams under server-generated keys. Put consumes exactly size
// bytes and publishes only complete objects. Open transfers ownership of its
// reader to the caller. Missing objects, including Delete, return apperr.NotFound.
// Implementations support concurrent calls and honor cancellation between reads.
// References are private to the backend and must never be used as public URLs.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (ref string, err error)
	Open(ctx context.Context, ref string) (io.ReadCloser, error)
	Delete(ctx context.Context, ref string) error
}

// Key builds an image key from database identifiers and a trusted image
// extension. Client file names must never be passed to a Store as keys.
func Key(userID, receiptID, imageID uuid.UUID, extension string) (string, error) {
	if userID == uuid.Nil || receiptID == uuid.Nil || imageID == uuid.Nil {
		return "", apperr.New(apperr.Invalid, "image storage identifiers are required")
	}
	switch extension {
	case "jpg", "jpeg", "png", "heic", "heif":
		return userID.String() + "/" + receiptID.String() + "/" + imageID.String() + "." + extension, nil
	default:
		return "", apperr.New(apperr.Invalid, "unsupported image storage extension")
	}
}

// ValidateKey rejects ambiguous paths on both Windows and Unix. Adapters also
// enforce filesystem containment; lexical checks alone cannot contain symlinks.
func ValidateKey(key string) error {
	if !fs.ValidPath(key) || key == "." {
		return apperr.New(apperr.Invalid, "invalid image storage key")
	}
	for _, part := range strings.Split(key, "/") {
		if strings.HasSuffix(part, ".") {
			return apperr.New(apperr.Invalid, "invalid image storage key")
		}
		for _, c := range part {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			default:
				return apperr.New(apperr.Invalid, "invalid image storage key")
			}
		}
		stem, _, _ := strings.Cut(strings.ToUpper(part), ".")
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" ||
			(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9') {
			return apperr.New(apperr.Invalid, "invalid image storage key")
		}
	}
	return nil
}
