package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/ingest"
)

// Ingestion holds the services behind receipt submission. A nil field
// leaves its operations answering with a service-unavailable problem.
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
// converting twenty HEIC photos one at a time takes about a minute. The
// optional `original` part needs no more time: it is valid beside exactly
// one file, so it never multiplies the work of a full request.
const uploadTimeout = 10 * time.Minute

// uploadPart is one collected part of an upload request. An original part
// points at the untouched photo that pairs with its file.
type uploadPart struct {
	name     string
	data     []byte
	original *uploadPart
}

// judgeOriginal returns the content of a read `original` part, or the
// problem that refuses the request when the bytes are not an accepted
// photo within the size limit of a file.
func judgeOriginal(name string, data []byte) (*uploadPart, *apperr.Error) {
	switch {
	case len(data) > maxUploadFileBytes:
		return nil, apperr.New(apperr.Invalid, "The original photo is larger than 15 MB.").WithField("original", "at most 15 MB")
	case len(data) == 0:
		return nil, apperr.New(apperr.Invalid, "The original photo is empty.").WithField("original", "a photo is required")
	}
	if ingest.Sniff(data) == "" {
		return nil, apperr.New(apperr.Invalid, "Only JPEG, PNG and HEIC/HEIF photos are accepted.").WithField("original", "accepted photo types only")
	}
	return &uploadPart{name: name, data: data}, nil
}

// combinationProblem refuses an `original` part sent with more than one
// file: the contract allows it only next to exactly one.
func combinationProblem() *apperr.Error {
	return apperr.New(apperr.Invalid, "Send the original photo with exactly one file.").WithField("original", "only with one file")
}

// UploadReceipts submits the uploaded files, one receipt each, and reports
// a result per file. A request with exactly one file may also carry one
// `original` part, the untouched photo the file was made from, which pairs
// with the file in either part order.
//
// Parts are collected before submission so that a request with more than
// the limit, or an invalid original, is rejected before any receipt is
// committed. A body that breaks after a complete part still commits that
// part's receipt and is reported as a bad request with no per-file results.
// The web client never sends more than twenty files.
func (h uploadsHandlers) UploadReceipts(ctx context.Context, request UploadReceiptsRequestObject) (UploadReceiptsResponseObject, error) {
	if h.uploads == nil {
		return nil, serviceUnavailable()
	}
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	// Collect the parts first so that a request with more than the limit,
	// or an invalid original, is rejected before any receipt is committed.
	// A broken body still commits any complete parts collected before it.
	// The parts are small enough to hold in memory for the duration of one
	// request.
	// bufferedOriginal is an original part seen before its file, waiting
	// for that file. The contract allows one original per request, so
	// this is the only original body ever held at once.
	var bufferedOriginal *uploadPart
	var collectedFiles []uploadPart
	// originalSeen remembers that an original entered the request, also
	// after it paired with its file, because its validity depends on how
	// many files the request carries.
	originalSeen := false
	readErr := error(nil)
	tooMany := false
	count := 0
	// held is a part the one-part lookahead already took from the body; the
	// loop consumes it before reading on.
	var held *multipart.Part
parse:
	for {
		var part *multipart.Part
		if held != nil {
			part, held = held, nil
		} else {
			next, err := request.Body.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				readErr = err
				break
			}
			part = next
		}
		switch part.FormName() {
		case "files":
			// A second file invalidates an original that is already in
			// play, whether it has paired with a file or still waits.
			if originalSeen && count > 0 {
				return nil, combinationProblem()
			}
			if count == maxUploadFiles {
				tooMany = true
				_ = part.Close()
				break parse
			}
			count++
			data, err := io.ReadAll(io.LimitReader(part, maxUploadFileBytes+1))
			_ = part.Close()
			if err != nil {
				readErr = err
				break parse
			}
			entry := uploadPart{name: part.FileName(), data: data}
			// An original seen before this file pairs with it.
			if bufferedOriginal != nil {
				entry.original, bufferedOriginal = bufferedOriginal, nil
			}
			// One-part lookahead: an original that follows the file pairs
			// with it, so either part order works.
			next, err := request.Body.NextPart()
			switch {
			case errors.Is(err, io.EOF):
			case err != nil:
				// The body broke after a complete part, which still commits.
				collectedFiles = append(collectedFiles, entry)
				readErr = err
				break parse
			case next.FormName() == "original":
				data, err := io.ReadAll(io.LimitReader(next, maxUploadFileBytes+1))
				_ = next.Close()
				if err != nil {
					collectedFiles = append(collectedFiles, entry)
					readErr = err
					break parse
				}
				original, problem := judgeOriginal(next.FileName(), data)
				if problem != nil {
					return nil, problem
				}
				if entry.original != nil {
					// The file already carries the original that came
					// before it; this one is a duplicate.
					return nil, apperr.New(apperr.Invalid, "Send at most one original photo.").WithField("original", "at most one original")
				}
				entry.original, originalSeen = original, true
				if count > 1 {
					return nil, combinationProblem()
				}
			default:
				held = next
			}
			collectedFiles = append(collectedFiles, entry)
		case "original":
			data, err := io.ReadAll(io.LimitReader(part, maxUploadFileBytes+1))
			_ = part.Close()
			if err != nil {
				readErr = err
				break parse
			}
			original, problem := judgeOriginal(part.FileName(), data)
			if problem != nil {
				return nil, problem
			}
			if originalSeen {
				return nil, apperr.New(apperr.Invalid, "Send at most one original photo.").WithField("original", "at most one original")
			}
			bufferedOriginal, originalSeen = original, true
			if count > 1 {
				return nil, combinationProblem()
			}
		default:
			// The contract has no other field; an extra one is skipped.
			_ = part.Close()
		}
	}
	// A request over the limit, or an invalid original, is rejected
	// outright: none of the files collected so far become receipts.
	if tooMany {
		return nil, apperr.New(apperr.Invalid, "Upload at most 20 files at once.").WithField("files", "at most 20 files")
	}
	// An original that reached no file makes the request invalid: its bytes
	// must not be dropped silently.
	if bufferedOriginal != nil && count > 0 {
		return nil, apperr.New(apperr.Invalid, "Send the original photo next to the file it belongs to.").WithField("original", "next to one file")
	}

	files := func(yield func(ingest.File) bool) {
		for _, c := range collectedFiles {
			file := ingest.File{Name: c.name, Body: bytes.NewReader(c.data)}
			if c.original != nil {
				file.Original = &ingest.File{Name: c.original.name, Body: bytes.NewReader(c.original.data)}
			}
			if !yield(file) {
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
