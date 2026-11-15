package receipts

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Image is a private stream; its caller must close Body.
type Image struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
}

// OpenImage checks receipt ownership before opening a display or original image.
func (s *Service) OpenImage(ctx context.Context, userID, receiptID, imageID uuid.UUID, original bool) (Image, error) {
	image, err := sqlc.New(s.db).GetOwnedReceiptImage(ctx, sqlc.GetOwnedReceiptImageParams{UserID: userID, ReceiptID: receiptID, ImageID: imageID})
	if err != nil {
		return Image{}, classify(err)
	}
	ref, contentType, size := image.StorageRef, image.ContentType, image.SizeBytes
	if original {
		if image.OriginalRef == nil {
			return Image{}, apperr.New(apperr.NotFound, "original image not found")
		}
		ref, contentType, size = *image.OriginalRef, *image.OriginalContentType, *image.OriginalSizeBytes
	}
	if s.images == nil {
		return Image{}, apperr.New(apperr.Unavailable, "image storage is unavailable")
	}
	body, err := s.images.Open(ctx, ref)
	if err != nil {
		return Image{}, storageError(err)
	}
	return Image{Body: body, ContentType: contentType, Size: size}, nil
}

func storageError(err error) error {
	if errors.Is(err, apperr.NotFound) {
		return err
	}
	return apperr.Wrap(apperr.Unavailable, "image storage is unavailable", err)
}

// Delete removes stored display and original objects, the job, and receipt-owned rows.
func (s *Service) Delete(ctx context.Context, userID, receiptID uuid.UUID) error {
	return s.transaction(ctx, func(q *unit) error {
		if _, err := q.LockReviewReceipt(ctx, sqlc.LockReviewReceiptParams{UserID: userID, ReceiptID: receiptID}); err != nil {
			return err
		}
		before, err := loadDetail(ctx, q.Queries, userID, receiptID)
		if err != nil {
			return err
		}
		if len(before.Images) > 0 && s.images == nil {
			return apperr.New(apperr.Unavailable, "image storage is unavailable")
		}
		for _, image := range before.Images {
			refs := []string{image.StorageRef}
			if image.OriginalRef != nil {
				refs = append(refs, *image.OriginalRef)
			}
			for _, ref := range refs {
				if err := s.images.Delete(ctx, ref); err != nil && !errors.Is(err, apperr.NotFound) {
					return storageError(err)
				}
			}
		}
		if err := q.DeleteReceiptJobs(ctx, sqlc.DeleteReceiptJobsParams{UserID: userID.String(), ReceiptID: receiptID.String()}); err != nil {
			return err
		}
		if err := q.DeleteReceiptData(ctx, sqlc.DeleteReceiptDataParams{UserID: userID, ReceiptID: receiptID}); err != nil {
			return err
		}
		return s.audit(ctx, q, userID, "receipt", receiptID, "deleted", before, nil)
	})
}
