package httpapi

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/ingest"
)

// Ingestion holds the services behind receipt submission. A nil field
// leaves its operations answering 501, as before they were built.
type Ingestion struct {
	Uploads Uploader
	Usage   UsageReporter
}

// Uploader is what the upload operation needs from the ingest service.
type Uploader interface {
	Submit(ctx context.Context, userID uuid.UUID, source ingest.Source, files iter.Seq[ingest.File]) []ingest.Result
}

// uploadsHandlers serves the operations of the uploads tag: receipt photo uploads.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type uploadsHandlers struct{ uploads Uploader }

// uploadTimeout replaces the server's read and write timeouts for one
// upload: twenty photos of 15 MB take minutes on a phone connection, and
// converting twenty HEIC photos one at a time takes about a minute.
const uploadTimeout = 10 * time.Minute

// UploadReceipts submits the uploaded files, one receipt each, and reports a result per file.
//
// The parts are handed to the ingest service as they arrive, so a request
// never holds more than one file in memory. The flip side is that a
// request-level problem found late, a twenty-first file or a broken body,
// fails the request after the files before it were already accepted; their
// receipts stay, and the web client never sends more than twenty files.
func (h uploadsHandlers) UploadReceipts(ctx context.Context, request UploadReceiptsRequestObject) (UploadReceiptsResponseObject, error) {
	if h.uploads == nil {
		return nil, notImplemented()
	}
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	var readErr error
	tooMany := false
	count := 0
	files := func(yield func(ingest.File) bool) {
		for {
			part, err := request.Body.NextPart()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				readErr = err
				return
			}
			if part.FormName() != "files" {
				// The contract has no other field; an extra one is skipped.
				_ = part.Close()
				continue
			}
			if count == maxUploadFiles {
				tooMany = true
				_ = part.Close()
				return
			}
			count++
			body := &partReader{part: part}
			more := yield(ingest.File{Name: part.FileName(), Body: body})
			_ = part.Close()
			if body.err != nil {
				// The request body itself broke; nothing after it is usable.
				readErr = body.err
				return
			}
			if !more {
				return
			}
		}
	}
	results := h.uploads.Submit(ctx, userID, ingest.SourceWeb, files)

	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(readErr, &tooLarge):
		return nil, readErr
	case readErr != nil:
		return nil, apperr.Wrap(apperr.Invalid, "The upload is not a valid multipart form.", readErr)
	case tooMany:
		return nil, apperr.New(apperr.Invalid, "Upload at most 20 files at once.").WithField("files", "at most 20 files")
	case len(results) == 0:
		return nil, apperr.New(apperr.Invalid, "Choose at least one photo to upload.").WithField("files", "at least one file is required")
	}

	response := UploadReceipts200JSONResponse{Results: make([]UploadResult, 0, len(results))}
	for _, result := range results {
		item := UploadResult{Index: result.Index, FileName: result.FileName, Outcome: UploadOutcome(result.Outcome)}
		if result.ReceiptID != uuid.Nil {
			id := result.ReceiptID
			item.ReceiptId = &id
		}
		if result.Outcome == ingest.OutcomeRejected {
			item.Error = &UploadError{Code: UploadErrorCode(result.Code), Message: result.Message}
		}
		response.Results = append(response.Results, item)
	}
	return response, nil
}

// partReader remembers why reading a part failed, so that a broken request
// body is answered as a bad request rather than as one unreadable file.
type partReader struct {
	part io.Reader
	err  error
}

func (r *partReader) Read(p []byte) (int, error) {
	n, err := r.part.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && r.err == nil {
		r.err = err
	}
	return n, err
}

// uploadDeadlines extends the connection deadlines of the upload operation
// beyond the server's defaults, which suit small JSON requests.
func uploadDeadlines(next StrictHandlerFunc, operationID string) StrictHandlerFunc {
	if operationID != "UploadReceipts" {
		return next
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		controller := http.NewResponseController(w)
		deadline := time.Now().Add(uploadTimeout)
		// A writer that cannot change deadlines, as in tests, keeps the
		// defaults.
		_ = controller.SetReadDeadline(deadline)
		_ = controller.SetWriteDeadline(deadline)
		return next(ctx, w, r, request)
	}
}
