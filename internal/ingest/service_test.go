package ingest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/ingest"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/storage"
	"github.com/enesoglu/receipt-tracker/internal/storage/local"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type counter struct{ n atomic.Int64 }

func (c *counter) Notify() { c.n.Add(1) }

// faultyStore wraps a real store and fails the operations the test chooses.
type faultyStore struct {
	storage.Store
	failPut func(key string) error
}

func (s *faultyStore) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	if s.failPut != nil {
		if err := s.failPut(key); err != nil {
			return "", err
		}
	}
	return s.Store.Put(ctx, key, r, size, contentType)
}

type fixture struct {
	pool     *pgxpool.Pool
	dir      string
	store    *faultyStore
	notified *counter
	service  *ingest.Service
}

func newUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{email}, DevLoginEmail: email}
	return testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
}

func newFixture(t *testing.T, converter ingest.Converter) fixture {
	t.Helper()
	pool := dbtest.New(t)
	dir := t.TempDir()
	store, err := local.New(t.Context(), dir)
	if err != nil {
		t.Fatalf("open local store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if converter == nil {
		converter = newConverter()
	}
	faulty := &faultyStore{Store: store}
	notified := &counter{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clock := func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }
	return fixture{
		pool: pool, dir: dir, store: faulty, notified: notified,
		service: ingest.New(pool, faulty, "local", converter, notified, logger, clock),
	}
}

func files(contents ...[]byte) iter.Seq[ingest.File] {
	return func(yield func(ingest.File) bool) {
		for i, content := range contents {
			if !yield(ingest.File{Name: "photo-" + string(rune('a'+i)) + ".bin", Body: bytes.NewReader(content)}) {
				return
			}
		}
	}
}

// photo returns a small distinct JPEG or PNG.
func photo(t testing.TB, n int, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 8))
	var v byte
	for i := range img.Pix {
		img.Pix[i] = v
		v += 37
	}
	// One inverted sample makes each photo's bytes unique.
	img.Pix[n%len(img.Pix)] ^= 0xff
	var buf bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatalf("encode photo: %v", err)
	}
	return buf.Bytes()
}

type imageRow struct {
	ReceiptID           uuid.UUID
	Sha256              []byte
	ContentType         string
	SizeBytes           int64
	StorageBackend      string
	StorageRef          string
	OriginalRef         *string
	OriginalContentType *string
	OriginalSizeBytes   *int64
}

func (f fixture) image(t *testing.T, receiptID uuid.UUID) imageRow {
	t.Helper()
	var row imageRow
	err := f.pool.QueryRow(t.Context(), `SELECT receipt_id, sha256, content_type, size_bytes, storage_backend, storage_ref,
		original_ref, original_content_type, original_size_bytes FROM receipt_images WHERE receipt_id = $1`, receiptID).Scan(
		&row.ReceiptID, &row.Sha256, &row.ContentType, &row.SizeBytes, &row.StorageBackend, &row.StorageRef,
		&row.OriginalRef, &row.OriginalContentType, &row.OriginalSizeBytes)
	if err != nil {
		t.Fatalf("load image row: %v", err)
	}
	return row
}

func (f fixture) object(t *testing.T, ref string) []byte {
	t.Helper()
	r, err := f.store.Open(t.Context(), ref)
	if err != nil {
		t.Fatalf("open stored object: %v", err)
	}
	data, err := io.ReadAll(r)
	if closeErr := r.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("read stored object: %v", err)
	}
	return data
}

func (f fixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// storedFiles counts the objects in the local store's directory.
func (f fixture) storedFiles(t *testing.T) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(f.dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk store: %v", err)
	}
	return n
}

// queuedReceipts claims every job and returns the receipt of each, in the
// order the worker would run them.
func (f fixture) queuedReceipts(t *testing.T) []uuid.UUID {
	t.Helper()
	queries := sqlc.New(f.pool)
	var ids []uuid.UUID
	for {
		job, err := queries.ClaimJob(t.Context(), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
		if err != nil {
			return ids
		}
		var payload jobs.ExtractReceipt
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		ids = append(ids, payload.ReceiptID)
	}
}

// summary is a result without the generated receipt id.
type summary struct {
	Index    int
	FileName string
	Outcome  ingest.Outcome
	Code     ingest.Code
}

func summarize(results []ingest.Result) []summary {
	out := make([]summary, 0, len(results))
	for _, r := range results {
		out = append(out, summary{Index: r.Index, FileName: r.FileName, Outcome: r.Outcome, Code: r.Code})
	}
	return out
}

func TestSubmit_CreatesQueuedReceiptsWithJobsInSubmissionOrder(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	var uploads [][]byte
	for i := range 10 {
		format := "jpeg"
		if i%3 == 0 {
			format = "png"
		}
		uploads = append(uploads, photo(t, i, format))
	}

	results := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(uploads...))

	var created []uuid.UUID
	for i, result := range results {
		if result.Index != i || result.Outcome != ingest.OutcomeCreated || result.ReceiptID == uuid.Nil || result.Code != "" {
			t.Fatalf("result %d = %+v, want created", i, result)
		}
		created = append(created, result.ReceiptID)

		row := f.image(t, result.ReceiptID)
		wantType := "image/jpeg"
		if i%3 == 0 {
			wantType = "image/png"
		}
		digest := sha256.Sum256(uploads[i])
		if row.ContentType != wantType || row.SizeBytes != int64(len(uploads[i])) || row.StorageBackend != "local" ||
			!bytes.Equal(row.Sha256, digest[:]) || row.OriginalRef != nil {
			t.Errorf("image row %d = %+v", i, row)
		}
		// JPEG and PNG uploads are stored exactly as they came.
		if !bytes.Equal(f.object(t, row.StorageRef), uploads[i]) {
			t.Errorf("stored object %d differs from the upload", i)
		}
	}
	if diff := cmp.Diff(created, f.queuedReceipts(t)); diff != "" {
		t.Errorf("job order mismatch (-want +got):\n%s", diff)
	}
	if got := f.notified.n.Load(); got != 10 {
		t.Errorf("notified the worker %d times, want 10", got)
	}

	var title, currency, status, source string
	if err := f.pool.QueryRow(t.Context(), "SELECT title, currency, status, source FROM receipts WHERE id = $1 AND user_id = $2",
		created[0], user).Scan(&title, &currency, &status, &source); err != nil {
		t.Fatalf("load receipt: %v", err)
	}
	if diff := cmp.Diff([]string{"Home shopping", "TRY", "queued", "web"}, []string{title, currency, status, source}); diff != "" {
		t.Errorf("receipt mismatch (-want +got):\n%s", diff)
	}
}

func TestSubmit_TakesTitleAndCurrencyFromUserSettings(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	if _, err := f.pool.Exec(t.Context(), "UPDATE user_settings SET default_receipt_title = 'Groceries', default_currency = 'EUR' WHERE user_id = $1", user); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	results := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(photo(t, 1, "jpeg")))
	var title, currency string
	if err := f.pool.QueryRow(t.Context(), "SELECT title, currency FROM receipts WHERE id = $1", results[0].ReceiptID).Scan(&title, &currency); err != nil {
		t.Fatalf("load receipt: %v", err)
	}
	if title != "Groceries" || currency != "EUR" {
		t.Errorf("receipt has title %q and currency %q, want the user's defaults", title, currency)
	}
}

func TestSubmit_RejectsBadFilesWhileTheRestSucceed(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	earlier := photo(t, 1, "jpeg")
	first := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(earlier))[0]

	good, other := photo(t, 2, "jpeg"), photo(t, 3, "png")
	oversized := append([]byte{0xff, 0xd8, 0xff}, make([]byte, ingest.MaxFileBytes)...)
	atLimit := append([]byte{0xff, 0xd8, 0xff}, make([]byte, ingest.MaxFileBytes-3)...)
	results := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(
		good,
		earlier, // uploaded in an earlier request
		good,    // the same bytes again in this request
		oversized,
		[]byte("%PDF-1.7 a document, not a photo"),
		nil,
		other,
		atLimit,
	))

	want := []summary{
		{0, "photo-a.bin", ingest.OutcomeCreated, ""},
		{1, "photo-b.bin", ingest.OutcomeDuplicate, ""},
		{2, "photo-c.bin", ingest.OutcomeDuplicate, ""},
		{3, "photo-d.bin", ingest.OutcomeRejected, ingest.CodeTooLarge},
		{4, "photo-e.bin", ingest.OutcomeRejected, ingest.CodeUnsupportedType},
		{5, "photo-f.bin", ingest.OutcomeRejected, ingest.CodeUnreadable},
		{6, "photo-g.bin", ingest.OutcomeCreated, ""},
		{7, "photo-h.bin", ingest.OutcomeCreated, ""},
	}
	if diff := cmp.Diff(want, summarize(results)); diff != "" {
		t.Fatalf("results mismatch (-want +got):\n%s", diff)
	}
	if results[1].ReceiptID != first.ReceiptID {
		t.Errorf("duplicate of an earlier upload names %v, want %v", results[1].ReceiptID, first.ReceiptID)
	}
	if results[2].ReceiptID != results[0].ReceiptID {
		t.Errorf("duplicate within the request names %v, want %v", results[2].ReceiptID, results[0].ReceiptID)
	}
	for _, i := range []int{3, 4, 5} {
		if results[i].ReceiptID != uuid.Nil || results[i].Message == "" {
			t.Errorf("rejected result %d = %+v, want no receipt and a message", i, results[i])
		}
	}
	// One earlier receipt and three new ones; nothing of the refused files.
	if got := f.count(t, "receipts"); got != 4 {
		t.Errorf("%d receipts, want 4", got)
	}
	if got := f.storedFiles(t); got != 4 {
		t.Errorf("%d stored objects, want 4", got)
	}
}

func TestSubmit_ConvertsHEICAndKeepsTheOriginal(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	upload := readFixture(t, "rotated-heifenc.heic")

	result := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(upload))[0]
	if result.Outcome != ingest.OutcomeCreated {
		t.Fatalf("result = %+v, want created", result)
	}
	row := f.image(t, result.ReceiptID)
	digest := sha256.Sum256(upload)
	if row.ContentType != "image/jpeg" || !bytes.Equal(row.Sha256, digest[:]) {
		t.Errorf("image row = %+v, want a JPEG keyed by the hash of the HEIC upload", row)
	}
	if row.OriginalRef == nil || *row.OriginalContentType != "image/heic" || *row.OriginalSizeBytes != int64(len(upload)) {
		t.Fatalf("image row = %+v, want the HEIC kept as the original", row)
	}
	if !bytes.Equal(f.object(t, *row.OriginalRef), upload) {
		t.Error("stored original differs from the upload")
	}
	converted := f.object(t, row.StorageRef)
	if row.SizeBytes != int64(len(converted)) {
		t.Errorf("size_bytes = %d, want the size of the JPEG, %d", row.SizeBytes, len(converted))
	}
	// The photo is stored upright: the rotation is applied, not recorded.
	want := layout{64, 128, [4]string{"blue", "red", "white", "green"}}
	if diff := cmp.Diff(want, layoutOf(decodeJPEG(t, converted))); diff != "" {
		t.Errorf("converted image mismatch (-want +got):\n%s", diff)
	}
}

func TestSubmit_TruncatedHEICIsRejectedAndStoresNothing(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	data := readFixture(t, "sample.heic")

	result := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(data[:len(data)/2]))[0]
	if result.Outcome != ingest.OutcomeRejected || result.Code != ingest.CodeUnreadable {
		t.Fatalf("result = %+v, want rejected as unreadable", result)
	}
	if got := f.storedFiles(t); got != 0 {
		t.Errorf("%d stored objects, want none", got)
	}
	if got := f.count(t, "receipts"); got != 0 {
		t.Errorf("%d receipts, want none", got)
	}
}

func TestSubmit_SameHEICTwiceIsDuplicate(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	data := readFixture(t, "sample.heic")
	results := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(data, data))

	if results[0].Outcome != ingest.OutcomeCreated || results[1].Outcome != ingest.OutcomeDuplicate || results[1].ReceiptID != results[0].ReceiptID {
		t.Errorf("results = %+v, want the second reported as a duplicate of the first", results)
	}
	again := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(data))[0]
	if again.Outcome != ingest.OutcomeDuplicate || again.ReceiptID != results[0].ReceiptID {
		t.Errorf("later upload = %+v, want a duplicate of %v", again, results[0].ReceiptID)
	}
}

// slowConverter records how many conversions run at once.
type slowConverter struct {
	running, peak atomic.Int32
	calls         atomic.Int32
}

func (c *slowConverter) Convert(_ context.Context, _ []byte) ([]byte, error) {
	c.calls.Add(1)
	now := c.running.Add(1)
	defer c.running.Add(-1)
	for {
		peak := c.peak.Load()
		if now <= peak || c.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	time.Sleep(100 * time.Millisecond)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestSubmit_ConvertsOneHEICAtATime(t *testing.T) {
	converter := &slowConverter{}
	f := newFixture(t, converter)
	alice := newUser(t, f.pool, "ada@example.com")
	bob := newUser(t, f.pool, "bob@example.com")

	var wg sync.WaitGroup
	results := make([]ingest.Result, 2)
	for i, user := range []uuid.UUID{alice, bob} {
		wg.Go(func() {
			results[i] = f.service.Submit(t.Context(), user, ingest.SourceWeb, files(readFixture(t, "sample.heic")))[0]
		})
	}
	wg.Wait()

	for i, result := range results {
		if result.Outcome != ingest.OutcomeCreated {
			t.Errorf("result %d = %+v, want created", i, result)
		}
	}
	if converter.calls.Load() != 2 || converter.peak.Load() != 1 {
		t.Errorf("%d conversions with up to %d at once, want 2 one after the other", converter.calls.Load(), converter.peak.Load())
	}
}

func TestSubmit_IsolatesUsers(t *testing.T) {
	f := newFixture(t, nil)
	alice := newUser(t, f.pool, "ada@example.com")
	bob := newUser(t, f.pool, "bob@example.com")
	upload := photo(t, 1, "jpeg")

	aliceResult := f.service.Submit(t.Context(), alice, ingest.SourceWeb, files(upload))[0]
	bobResult := f.service.Submit(t.Context(), bob, ingest.SourceWeb, files(upload))[0]

	// The same photo is new to Bob: he learns nothing about Alice's receipt.
	if bobResult.Outcome != ingest.OutcomeCreated || bobResult.ReceiptID == aliceResult.ReceiptID {
		t.Fatalf("Bob's upload = %+v, want his own new receipt", bobResult)
	}
	var owner uuid.UUID
	if err := f.pool.QueryRow(t.Context(), "SELECT user_id FROM receipts WHERE id = $1", bobResult.ReceiptID).Scan(&owner); err != nil {
		t.Fatalf("load receipt: %v", err)
	}
	if owner != bob {
		t.Errorf("Bob's receipt belongs to %v", owner)
	}
	again := f.service.Submit(t.Context(), bob, ingest.SourceWeb, files(upload))[0]
	if again.Outcome != ingest.OutcomeDuplicate || again.ReceiptID != bobResult.ReceiptID {
		t.Errorf("Bob's second upload = %+v, want a duplicate of his own receipt", again)
	}
}

func TestSubmit_DeletesStoredObjectsWhenStoringTheOriginalFails(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	f.store.failPut = func(key string) error {
		if strings.HasSuffix(key, ".heic") {
			return errors.New("disk full")
		}
		return nil
	}
	result := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(readFixture(t, "sample.heic")))[0]
	if result.Outcome != ingest.OutcomeRejected || result.Code != ingest.CodeStorageUnavailable {
		t.Fatalf("result = %+v, want rejected as storage unavailable", result)
	}
	if strings.Contains(result.Message, "disk full") {
		t.Errorf("message %q exposes the internal error", result.Message)
	}
	if got := f.storedFiles(t); got != 0 {
		t.Errorf("%d stored objects left behind, want none", got)
	}
}

func TestSubmit_DeletesStoredObjectsWhenTheTransactionFails(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	// The schema accepts only known sources, so the insert fails after the
	// images were stored.
	result := f.service.Submit(t.Context(), user, ingest.Source("carrier-pigeon"), files(readFixture(t, "sample.heic")))[0]
	if result.Outcome != ingest.OutcomeRejected || result.Code != ingest.CodeStorageUnavailable {
		t.Fatalf("result = %+v, want rejected as storage unavailable", result)
	}
	if got := f.storedFiles(t); got != 0 {
		t.Errorf("%d stored objects without a receipt, want none", got)
	}
	if got := f.count(t, "jobs"); got != 0 {
		t.Errorf("%d jobs, want none", got)
	}
}

func TestSubmit_PassesOnStorageAdvice(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	f.store.failPut = func(string) error {
		return apperr.New(apperr.Unavailable, "Connect Google Drive to use image storage.").WithCode("storage_not_connected")
	}
	result := f.service.Submit(t.Context(), user, ingest.SourceWeb, files(photo(t, 1, "jpeg")))[0]
	if result.Code != ingest.CodeStorageUnavailable || result.Message != "Connect Google Drive to use image storage." {
		t.Errorf("result = %+v, want the storage's advice", result)
	}
}

// failingReader returns some bytes and then an error, like a connection
// that drops mid-upload.
type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, errors.New("connection reset")
	}
	r.sent = true
	return copy(p, []byte{0xff, 0xd8, 0xff, 0xe0}), nil
}

func TestSubmit_UnreadableBodyIsRejected(t *testing.T) {
	f := newFixture(t, nil)
	user := newUser(t, f.pool, "ada@example.com")
	seq := func(yield func(ingest.File) bool) {
		if !yield(ingest.File{Name: "dropped.jpg", Body: &failingReader{}}) {
			return
		}
		yield(ingest.File{Name: "fine.jpg", Body: bytes.NewReader(photo(t, 1, "jpeg"))})
	}
	results := f.service.Submit(t.Context(), user, ingest.SourceWeb, seq)
	want := []summary{
		{0, "dropped.jpg", ingest.OutcomeRejected, ingest.CodeUnreadable},
		{1, "fine.jpg", ingest.OutcomeCreated, ""},
	}
	if diff := cmp.Diff(want, summarize(results)); diff != "" {
		t.Errorf("results mismatch (-want +got):\n%s", diff)
	}
}
