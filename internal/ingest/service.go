package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/storage"
)

// MaxFileBytes is the largest file Submit accepts: 15 MB.
const MaxFileBytes = 15 << 20

// Source names the channel a receipt arrived through. The values mirror the
// receipts_source_check constraint.
type Source string

// SourceWeb is an upload through the web application's API.
const SourceWeb Source = "web"

// File is one submitted file. Submit reads Body to the end, or until the
// size limit, before it asks for the next file, so a caller can hand over
// the parts of a streamed request one by one.
type File struct {
	// Name is the file name as the client gave it. It is only reported
	// back, never used as a path or key.
	Name string
	Body io.Reader
}

// Outcome says what happened to one file.
type Outcome string

// The outcomes of a file.
const (
	// OutcomeCreated: a receipt was created and queued for extraction.
	OutcomeCreated Outcome = "created"
	// OutcomeDuplicate: this user submitted the same bytes before.
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeRejected: the file was refused; Code says why.
	OutcomeRejected Outcome = "rejected"
)

// Code says why a file was rejected.
type Code string

// The reasons for rejecting a file.
const (
	CodeUnsupportedType    Code = "unsupported_type"
	CodeTooLarge           Code = "too_large"
	CodeUnreadable         Code = "unreadable"
	CodeStorageUnavailable Code = "storage_unavailable"
)

// Result is what happened to one file, in submission order.
type Result struct {
	// Index is the position of the file in the submission, from 0.
	Index    int
	FileName string
	Outcome  Outcome
	// ReceiptID is the new receipt for OutcomeCreated, the existing one for
	// OutcomeDuplicate, and uuid.Nil otherwise.
	ReceiptID uuid.UUID
	// Code and Message are set for OutcomeRejected. Message is written for
	// the user.
	Code    Code
	Message string
}

// Notifier is told when a job has been committed, so that the worker can
// start without waiting for its next poll.
type Notifier interface {
	Notify()
}

// database is what Submit needs from the connection pool.
type database interface {
	sqlc.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Service creates queued receipts from submitted files. It knows nothing
// about HTTP: the upload endpoint and any later channel call [Service.Submit].
type Service struct {
	db        database
	store     storage.Store
	backend   string
	converter Converter
	notifier  Notifier
	logger    *slog.Logger
	now       func() time.Time
	// convertSlot admits one conversion at a time: decoding a large HEIC
	// is the most memory-hungry step of the server.
	convertSlot chan struct{}
}

// New returns a service that stores images in store, which is the backend
// named by backend ("local" or "drive", recorded on each image row).
func New(db database, store storage.Store, backend string, converter Converter, notifier Notifier, logger *slog.Logger, now func() time.Time) *Service {
	return &Service{
		db: db, store: store, backend: backend, converter: converter, notifier: notifier,
		logger: logger, now: now, convertSlot: make(chan struct{}, 1),
	}
}

// Submit judges and stores each file on its own and returns one result per
// file, in order. Per file: the type is sniffed from the bytes, the size is
// limited, the SHA-256 of the uploaded bytes is computed while reading, a
// file this user submitted before is reported as a duplicate, a HEIC/HEIF
// image is converted to JPEG (the original is kept), the image is stored,
// and the receipt, its image row and its extraction job are created in one
// transaction. Nothing of a refused file is kept.
func (s *Service) Submit(ctx context.Context, userID uuid.UUID, source Source, files iter.Seq[File]) []Result {
	results := []Result{}
	for file := range files {
		result := s.submit(ctx, userID, source, file)
		result.Index, result.FileName = len(results), file.Name
		results = append(results, result)
	}
	return results
}

func rejected(code Code, message string) Result {
	return Result{Outcome: OutcomeRejected, Code: code, Message: message}
}

// object is one image to store.
type object struct {
	data        []byte
	contentType string
	extension   string
}

func (s *Service) submit(ctx context.Context, userID uuid.UUID, source Source, file File) Result {
	data, sum, tooLarge, err := read(file.Body)
	switch {
	case err != nil:
		return rejected(CodeUnreadable, "The file could not be read completely.")
	case tooLarge:
		return rejected(CodeTooLarge, "The file is larger than 15 MB.")
	case len(data) == 0:
		return rejected(CodeUnreadable, "The file is empty.")
	}
	contentType := sniff(data[:min(len(data), sniffLength)])
	if contentType == "" {
		return rejected(CodeUnsupportedType, "Only JPEG, PNG and HEIC/HEIF photos are accepted.")
	}

	existing, err := sqlc.New(s.db).FindReceiptByImageHash(ctx, sqlc.FindReceiptByImageHashParams{UserID: userID, Sha256: sum})
	switch {
	case err == nil:
		return Result{Outcome: OutcomeDuplicate, ReceiptID: existing}
	case !errors.Is(err, pgx.ErrNoRows):
		s.logger.Error("look up duplicate receipt", "user_id", userID, "error", err)
		return unavailable(err)
	}

	primary := object{data: data, contentType: contentType, extension: "png"}
	var original *object
	switch contentType {
	case typeJPEG:
		primary.extension = "jpg"
	case typeHEIC, typeHEIF:
		converted, err := s.convert(ctx, data)
		if err != nil {
			s.logger.Info("heic conversion refused", "user_id", userID, "size_bytes", len(data), "error", err)
			return rejected(CodeUnreadable, "The photo could not be decoded. It may be damaged or of a kind that is not supported.")
		}
		extension := "heic"
		if contentType == typeHEIF {
			extension = "heif"
		}
		original = &object{data: data, contentType: contentType, extension: extension}
		primary = object{data: converted, contentType: typeJPEG, extension: "jpg"}
	}

	receiptID, err := s.save(ctx, userID, source, sum, primary, original)
	var duplicate *duplicateError
	switch {
	case errors.As(err, &duplicate):
		return Result{Outcome: OutcomeDuplicate, ReceiptID: duplicate.receiptID}
	case err != nil:
		s.logger.Error("save receipt", "user_id", userID, "error", err)
		return unavailable(err)
	}
	s.notifier.Notify()
	return Result{Outcome: OutcomeCreated, ReceiptID: receiptID}
}

// unavailable reports a failure to keep the file. The message of a
// classified storage error, such as Drive not being connected, tells the
// user what to do; anything else gets a generic message.
func unavailable(err error) Result {
	var classified *apperr.Error
	if errors.As(err, &classified) && classified.Kind == apperr.Unavailable && classified.Message != "" {
		return rejected(CodeStorageUnavailable, classified.Message)
	}
	return rejected(CodeStorageUnavailable, "The file could not be saved right now. Try again later.")
}

// read returns the body and the SHA-256 of exactly the bytes read, which
// is computed while reading. It reads at most one byte past the limit, so an
// oversized file costs no more memory than an accepted one, and reports it
// with tooLarge.
func read(body io.Reader) (data, sum []byte, tooLarge bool, err error) {
	digest := sha256.New()
	data, err = io.ReadAll(io.LimitReader(io.TeeReader(body, digest), MaxFileBytes+1))
	if err != nil {
		return nil, nil, false, fmt.Errorf("read file: %w", err)
	}
	if len(data) > MaxFileBytes {
		return nil, nil, true, nil
	}
	return data, digest.Sum(nil), false, nil
}

// convert runs the converter once at a time.
func (s *Service) convert(ctx context.Context, data []byte) ([]byte, error) {
	select {
	case s.convertSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for conversion: %w", ctx.Err())
	}
	defer func() { <-s.convertSlot }()
	return s.converter.Convert(ctx, data)
}

// duplicateError reports that the same bytes were committed by a concurrent
// submission between the duplicate check and the insert.
type duplicateError struct{ receiptID uuid.UUID }

func (e *duplicateError) Error() string { return "duplicate image" }

// save stores the images and then commits the receipt, its image row and its
// job together. Objects stored for a receipt that was not committed are
// deleted again.
func (s *Service) save(ctx context.Context, userID uuid.UUID, source Source, sum []byte, primary object, original *object) (uuid.UUID, error) {
	receiptID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate receipt id: %w", err)
	}
	imageID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate image id: %w", err)
	}
	var stored []string
	committed := false
	defer func() {
		if !committed {
			s.discard(ctx, userID, receiptID, stored)
		}
	}()
	put := func(img object) (string, error) {
		key, err := storage.Key(userID, receiptID, imageID, img.extension)
		if err != nil {
			return "", err
		}
		ref, err := s.store.Put(ctx, key, bytes.NewReader(img.data), int64(len(img.data)), img.contentType)
		if err != nil {
			return "", fmt.Errorf("store image: %w", err)
		}
		stored = append(stored, ref)
		return ref, nil
	}
	ref, err := put(primary)
	if err != nil {
		return uuid.Nil, err
	}
	params := sqlc.CreateReceiptImageParams{
		ID: imageID, UserID: userID, ReceiptID: receiptID, Sha256: sum,
		ContentType: primary.contentType, SizeBytes: int64(len(primary.data)),
		StorageBackend: s.backend, StorageRef: ref,
	}
	if original != nil {
		originalRef, err := put(*original)
		if err != nil {
			return uuid.Nil, err
		}
		size := int64(len(original.data))
		params.OriginalRef, params.OriginalContentType, params.OriginalSizeBytes = &originalRef, &original.contentType, &size
	}

	err = s.commit(ctx, userID, source, receiptID, params)
	if isUniqueViolation(err) {
		existing, lookupErr := sqlc.New(s.db).FindReceiptByImageHash(ctx, sqlc.FindReceiptByImageHashParams{UserID: userID, Sha256: sum})
		if lookupErr == nil {
			return uuid.Nil, &duplicateError{receiptID: existing}
		}
		return uuid.Nil, errors.Join(err, lookupErr)
	}
	if err != nil {
		return uuid.Nil, err
	}
	committed = true
	return receiptID, nil
}

func (s *Service) commit(ctx context.Context, userID uuid.UUID, source Source, receiptID uuid.UUID, row sqlc.CreateReceiptImageParams) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin receipt transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := sqlc.New(tx)
	now := s.now().UTC()

	title, currency := "Home shopping", "TRY"
	defaults, err := queries.GetUploadDefaults(ctx, userID)
	switch {
	case err == nil:
		title, currency = defaults.DefaultReceiptTitle, defaults.DefaultCurrency
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("load upload defaults: %w", err)
	}
	if err := queries.CreateQueuedReceipt(ctx, sqlc.CreateQueuedReceiptParams{
		ID: receiptID, UserID: userID, Title: title, Currency: currency, Source: string(source), Now: now,
	}); err != nil {
		return fmt.Errorf("create receipt: %w", err)
	}
	row.Now = now
	if err := queries.CreateReceiptImage(ctx, row); err != nil {
		return fmt.Errorf("create receipt image: %w", err)
	}
	if _, err := jobs.Enqueue(ctx, tx, jobs.KindExtractReceipt, jobs.ExtractReceipt{UserID: userID, ReceiptID: receiptID}, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit receipt: %w", err)
	}
	return nil
}

// discard deletes objects whose receipt was never committed. It runs even
// when the request was canceled, because the objects would otherwise stay
// behind without a row that points at them.
func (s *Service) discard(ctx context.Context, userID, receiptID uuid.UUID, refs []string) {
	if len(refs) == 0 {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for _, ref := range refs {
		if err := s.store.Delete(cleanupCtx, ref); err != nil {
			s.logger.Error("delete uncommitted image", "user_id", userID, "receipt_id", receiptID, "error", err)
		}
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "receipt_images_user_id_sha256_key"
}
