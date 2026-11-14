package extract_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/extract/azure"
	"github.com/enesoglu/receipt-tracker/internal/ingest"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/storage/local"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

// clock drives the worker and the Azure client: Sleep advances it at once.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Advance(d)
	return nil
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// submission is what the fake Azure received.
type submission struct {
	at          time.Time
	contentType string
	body        []byte
}

// fakeAzure accepts every submission and answers the first poll with the
// synthetic result, unless refuse says otherwise.
type fakeAzure struct {
	server      *httptest.Server
	mu          sync.Mutex
	submissions []submission
	refuse      func(n int) (status int, body string)
}

func newFakeAzure(t *testing.T, c *clock) *fakeAzure {
	t.Helper()
	result, err := os.ReadFile("azure/testdata/receipt-2024-11-30.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	f := &fakeAzure{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write(result)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		n := len(f.submissions)
		f.submissions = append(f.submissions, submission{at: c.Now(), contentType: r.Header.Get("Content-Type"), body: body})
		refuse := f.refuse
		f.mu.Unlock()
		if refuse != nil {
			if status, message := refuse(n); status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(message))
				return
			}
		}
		w.Header().Set("Operation-Location", f.server.URL+"/documentintelligence/documentModels/prebuilt-receipt/analyzeResults/"+uuid.NewString()+"?api-version=2024-11-30")
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeAzure) received() []submission {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]submission(nil), f.submissions...)
}

// flakyResults fails the first n calls, then stores the result.
type flakyResults struct {
	next  extract.ResultHandler
	fails int
	calls int
}

func (r *flakyResults) ApplyResult(ctx context.Context, userID, receiptID uuid.UUID, result extract.Result) error {
	r.calls++
	if r.calls <= r.fails {
		return errors.New("database unavailable")
	}
	return r.next.ApplyResult(ctx, userID, receiptID, result)
}

type pipeline struct {
	pool    *pgxpool.Pool
	clock   *clock
	azure   *fakeAzure
	store   *local.Store
	ingest  *ingest.Service
	worker  *jobs.Worker
	usage   *extract.Usage
	results *flakyResults
	alice   uuid.UUID
	bob     uuid.UUID
}

type noNotify struct{}

func (noNotify) Notify() {}

func newPipeline(t *testing.T, monthlyPages int) *pipeline {
	t.Helper()
	pool := dbtest.New(t)
	c := &clock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := local.New(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fake := newFakeAzure(t, c)
	usage := extract.NewUsage(pool, extract.ProviderAzure, monthlyPages, c.Now)
	client, err := azure.New(azure.Options{Endpoint: fake.server.URL, Key: "synthetic-key", HTTP: fake.server.Client(), Clock: c, Meter: usage, Logger: logger})
	if err != nil {
		t.Fatalf("new azure client: %v", err)
	}
	results := &flakyResults{next: extract.NewStoreResult(pool, c.Now)}
	worker := jobs.New(pool, logger, c.Now, jobs.Options{Backoff: func(int) time.Duration { return time.Minute }})
	worker.Register(jobs.KindExtractReceipt, extract.NewJob(pool, store, client, usage, results, logger, c.Now))
	newUser := func(email string) uuid.UUID {
		cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{email}, DevLoginEmail: email}
		return testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	}
	return &pipeline{
		pool: pool, clock: c, azure: fake, store: store, worker: worker, usage: usage, results: results,
		ingest: ingest.New(pool, store, "local", ingest.NewHEICConverter(logger), noNotify{}, logger, c.Now),
		alice:  newUser("alice@example.com"), bob: newUser("bob@example.com"),
	}
}

func (p *pipeline) upload(t *testing.T, user uuid.UUID, contents ...[]byte) []uuid.UUID {
	t.Helper()
	files := iter.Seq[ingest.File](func(yield func(ingest.File) bool) {
		for _, content := range contents {
			if !yield(ingest.File{Name: "receipt.jpg", Body: bytes.NewReader(content)}) {
				return
			}
		}
	})
	var ids []uuid.UUID
	for _, result := range p.ingest.Submit(t.Context(), user, ingest.SourceWeb, files) {
		if result.Outcome != ingest.OutcomeCreated {
			t.Fatalf("upload result = %+v, want created", result)
		}
		ids = append(ids, result.ReceiptID)
	}
	return ids
}

// runAll processes jobs until none is runnable now.
func (p *pipeline) runAll(t *testing.T) {
	t.Helper()
	for {
		processed, err := p.worker.ProcessNext(t.Context())
		if err != nil {
			t.Fatalf("process job: %v", err)
		}
		if !processed {
			return
		}
	}
}

type receiptState struct {
	Status        string
	FailureReason string
	Total         string
	Lines         int
}

func (p *pipeline) receipt(t *testing.T, id uuid.UUID) receiptState {
	t.Helper()
	var s receiptState
	var reason, total *string
	err := p.pool.QueryRow(t.Context(), `SELECT status, failure_reason, total::text,
		(SELECT count(*) FROM line_items l WHERE l.receipt_id = r.id) FROM receipts r WHERE id = $1`, id).Scan(&s.Status, &reason, &total, &s.Lines)
	if err != nil {
		t.Fatalf("load receipt: %v", err)
	}
	if reason != nil {
		s.FailureReason = *reason
	}
	if total != nil {
		s.Total = *total
	}
	return s
}

func (p *pipeline) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := p.pool.QueryRow(t.Context(), query).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// photo returns a distinct small JPEG.
func photo(t *testing.T, n int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var v byte
	for i := range img.Pix {
		img.Pix[i] = v
		v += 29
	}
	img.Pix[(n*4)%len(img.Pix)] ^= 0xff // a red sample: JPEG drops alpha
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func TestPipeline_TenUploadsExtractedInUploadOrderOneSecondApart(t *testing.T) {
	p := newPipeline(t, 500)
	var uploads [][]byte
	for i := range 10 {
		uploads = append(uploads, photo(t, i))
	}
	ids := p.upload(t, p.alice, uploads...)

	p.runAll(t)

	received := p.azure.received()
	if len(received) != 10 {
		t.Fatalf("Azure received %d submissions, want 10", len(received))
	}
	for i, got := range received {
		if !bytes.Equal(got.body, uploads[i]) {
			t.Errorf("submission %d is not upload %d: files were not processed in upload order", i, i)
		}
		if i > 0 {
			if gap := got.at.Sub(received[i-1].at); gap < time.Second {
				t.Errorf("submission %d came %v after the previous one, want at least 1s", i, gap)
			}
		}
	}
	for i, id := range ids {
		want := receiptState{Status: "needs_review", Total: "70.00", Lines: 4}
		if diff := cmp.Diff(want, p.receipt(t, id)); diff != "" {
			t.Errorf("receipt %d mismatch (-want +got):\n%s", i, diff)
		}
	}
	if got := p.count(t, "SELECT count(*) FROM extractions WHERE provider = 'azure' AND model = 'prebuilt-receipt'"); got != 10 {
		t.Errorf("%d extractions saved, want 10", got)
	}
	report, err := p.usage.Report(t.Context())
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if diff := cmp.Diff(extract.Report{Provider: "azure", Period: "2026-11", Used: 10, Limit: 500}, report); diff != "" {
		t.Errorf("usage mismatch (-want +got):\n%s", diff)
	}
}

func TestPipeline_StoresExtractedFieldsAndLines(t *testing.T) {
	p := newPipeline(t, 500)
	id := p.upload(t, p.alice, photo(t, 1))[0]
	p.runAll(t)

	var storeName, currency, purchasedTime string
	var subtotal, tax string
	if err := p.pool.QueryRow(t.Context(), `SELECT s.name, r.currency, r.purchased_time::text, r.subtotal::text, r.tax_total::text
		FROM receipts r JOIN stores s ON s.id = r.store_id AND s.user_id = r.user_id WHERE r.id = $1 AND r.purchased_on IS NULL`, id).Scan(
		&storeName, &currency, &purchasedTime, &subtotal, &tax); err != nil {
		t.Fatalf("load receipt: %v", err)
	}
	if diff := cmp.Diff([]string{"SYNTHETIC MARKET", "TRY", "18:42:07", "63.74", "6.26"}, []string{storeName, currency, purchasedTime, subtotal, tax}); diff != "" {
		t.Errorf("receipt fields mismatch (-want +got):\n%s", diff)
	}
	rows, err := p.pool.Query(t.Context(), `SELECT position, raw_name, name, coalesce(quantity::text, ''), coalesce(quantity_unit, ''),
		coalesce(unit_price::text, ''), coalesce(line_total::text, ''), coalesce(product_code, ''), match_status
		FROM line_items WHERE receipt_id = $1 ORDER BY position`, id)
	if err != nil {
		t.Fatalf("load lines: %v", err)
	}
	defer rows.Close()
	var got [][]string
	for rows.Next() {
		var position int
		line := make([]string, 8)
		if err := rows.Scan(&position, &line[0], &line[1], &line[2], &line[3], &line[4], &line[5], &line[6], &line[7]); err != nil {
			t.Fatalf("scan line: %v", err)
		}
		got = append(got, line)
	}
	want := [][]string{
		{"SUT 1LT", "SUT 1LT", "2.000", "AD", "32.50", "65.00", "8690000000012", "unmatched"},
		{"EKMEK", "EKMEK", "", "", "", "15.00", "", "unmatched"},
		{"INDIRIM", "INDIRIM", "", "", "", "-10.00", "", "unmatched"},
		{"", "Unreadable item", "", "", "", "", "", "unmatched"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("line items mismatch (-want +got):\n%s", diff)
	}
}

func TestPipeline_HEICReachesTheExtractorAsJPEG(t *testing.T) {
	p := newPipeline(t, 500)
	heic, err := os.ReadFile("../ingest/testdata/sample.heic")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	p.upload(t, p.alice, heic)
	p.runAll(t)

	received := p.azure.received()
	if len(received) != 1 || received[0].contentType != "image/jpeg" {
		t.Fatalf("Azure received %+v, want one JPEG", received)
	}
	if _, err := jpeg.Decode(bytes.NewReader(received[0].body)); err != nil {
		t.Errorf("submitted bytes are not a JPEG: %v", err)
	}
}

func TestPipeline_LargeImageSentUnderFourMegabytesOriginalUntouched(t *testing.T) {
	p := newPipeline(t, 500)
	img := image.NewRGBA(image.Rect(0, 0, 1460, 1460))
	if _, err := rand.Read(img.Pix); err != nil {
		t.Fatalf("fill image: %v", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	upload := buf.Bytes()
	if len(upload) < 6<<20 {
		t.Fatalf("test image has %d bytes, want at least 6 MB", len(upload))
	}
	id := p.upload(t, p.alice, upload)[0]
	p.runAll(t)

	received := p.azure.received()
	if len(received) != 1 || len(received[0].body) >= 4<<20 {
		t.Fatalf("Azure received %d submissions, the first of %d bytes; want one under 4 MB", len(received), len(received[0].body))
	}
	var ref string
	if err := p.pool.QueryRow(t.Context(), "SELECT storage_ref FROM receipt_images WHERE receipt_id = $1", id).Scan(&ref); err != nil {
		t.Fatalf("load image: %v", err)
	}
	reader, err := p.store.Open(t.Context(), ref)
	if err != nil {
		t.Fatalf("open stored image: %v", err)
	}
	stored, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatalf("read stored image: %v", err)
	}
	if !bytes.Equal(stored, upload) {
		t.Error("the stored original changed")
	}
}

func TestPipeline_QuotaExhaustedWaitsForNextMonth(t *testing.T) {
	p := newPipeline(t, 1)
	if err := p.usage.Count(t.Context(), 1); err != nil {
		t.Fatalf("count: %v", err)
	}
	id := p.upload(t, p.alice, photo(t, 1))[0]

	p.runAll(t)

	if got := p.receipt(t, id).Status; got != "waiting_quota" {
		t.Errorf("receipt status = %q, want waiting_quota", got)
	}
	if got := len(p.azure.received()); got != 0 {
		t.Errorf("Azure received %d submissions while the allowance was used up", got)
	}
	var status string
	var runAfter time.Time
	var attempts int
	if err := p.pool.QueryRow(t.Context(), "SELECT status, run_after, attempts FROM jobs").Scan(&status, &runAfter, &attempts); err != nil {
		t.Fatalf("load job: %v", err)
	}
	want := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if status != "queued" || !runAfter.Equal(want) || attempts != 0 {
		t.Errorf("job = %s at %v after %d attempts, want queued for %v with no attempt used", status, runAfter, attempts, want)
	}

	// In December the allowance is new and the receipt is read.
	p.clock.Advance(want.Sub(p.clock.Now()))
	p.runAll(t)
	if got := p.receipt(t, id).Status; got != "needs_review" {
		t.Errorf("receipt status in December = %q, want needs_review", got)
	}
}

func TestPipeline_PermanentErrorFailsReceipt(t *testing.T) {
	p := newPipeline(t, 500)
	p.azure.refuse = func(int) (int, string) {
		return http.StatusBadRequest, `{"error":{"code":"InvalidRequest","message":"synthetic","innererror":{"code":"InvalidContent"}}}`
	}
	id := p.upload(t, p.alice, photo(t, 1))[0]
	p.runAll(t)

	got := p.receipt(t, id)
	if got.Status != "failed" || got.FailureReason != "The extraction service could not read this image." {
		t.Errorf("receipt = %+v, want failed with a reason for the user", got)
	}
	if n := p.count(t, "SELECT count(*) FROM jobs WHERE status = 'failed' AND attempts = 1"); n != 1 {
		t.Errorf("%d jobs failed after one attempt, want 1", n)
	}
	if n := p.count(t, "SELECT count(*) FROM extractions"); n != 0 {
		t.Errorf("%d extractions saved, want none", n)
	}
}

func TestPipeline_RetryableErrorsRequeueThenFailWithGenericReason(t *testing.T) {
	p := newPipeline(t, 500)
	p.azure.refuse = func(int) (int, string) {
		return http.StatusServiceUnavailable, `{"error":{"code":"ServiceUnavailable"}}`
	}
	id := p.upload(t, p.alice, photo(t, 1))[0]

	p.runAll(t)
	if got := p.receipt(t, id).Status; got != "queued" {
		t.Fatalf("receipt status after a retryable failure = %q, want queued", got)
	}
	for range jobs.MaxAttempts {
		p.clock.Advance(time.Hour)
		p.runAll(t)
	}
	got := p.receipt(t, id)
	if got.Status != "failed" || got.FailureReason != "Reading the receipt kept failing. Reprocess it later." {
		t.Errorf("receipt = %+v, want failed after the last attempt", got)
	}
	if strings.Contains(got.FailureReason, "503") || strings.Contains(got.FailureReason, "ServiceUnavailable") {
		t.Errorf("failure reason %q shows technical detail", got.FailureReason)
	}
}

func TestPipeline_CrashMidJobIsRecoveredAndRetried(t *testing.T) {
	p := newPipeline(t, 500)
	id := p.upload(t, p.alice, photo(t, 1))[0]
	// The previous process claimed the job, set the receipt to processing
	// and stopped.
	if _, err := sqlc.New(p.pool).ClaimJob(t.Context(), p.clock.Now()); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := sqlc.New(p.pool).SetExtractionStatus(t.Context(), sqlc.SetExtractionStatusParams{Status: "processing", Now: p.clock.Now(), UserID: p.alice, ReceiptID: id}); err != nil {
		t.Fatalf("set status: %v", err)
	}

	if _, err := p.worker.Recover(t.Context()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	p.runAll(t)

	if got := p.receipt(t, id).Status; got != "needs_review" {
		t.Errorf("receipt status = %q, want needs_review", got)
	}
	if n := p.count(t, "SELECT count(*) FROM jobs WHERE status = 'succeeded' AND attempts = 2"); n != 1 {
		t.Errorf("%d jobs succeeded on their second attempt, want 1", n)
	}
}

func TestPipeline_RetryAfterHandlerFailureReusesSavedResult(t *testing.T) {
	p := newPipeline(t, 500)
	p.results.fails = 1
	id := p.upload(t, p.alice, photo(t, 1))[0]

	p.runAll(t)
	p.clock.Advance(time.Hour)
	p.runAll(t)

	if got := p.receipt(t, id).Status; got != "needs_review" {
		t.Errorf("receipt status = %q, want needs_review", got)
	}
	if got := len(p.azure.received()); got != 1 {
		t.Errorf("Azure received %d submissions, want 1: the retry must reuse the saved result", got)
	}
	report, err := p.usage.Report(t.Context())
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if report.Used != 1 {
		t.Errorf("used %d pages, want 1", report.Used)
	}
}

func TestPipeline_JobForAnotherUsersReceiptDoesNothing(t *testing.T) {
	p := newPipeline(t, 500)
	id := p.upload(t, p.alice, photo(t, 1))[0]
	// A job naming Bob as the owner of Alice's receipt finds nothing.
	if _, err := jobs.Enqueue(t.Context(), p.pool, jobs.KindExtractReceipt, jobs.ExtractReceipt{UserID: p.bob, ReceiptID: id}, p.clock.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := p.pool.Exec(t.Context(), "DELETE FROM jobs WHERE payload->>'user_id' = $1", p.alice.String()); err != nil {
		t.Fatalf("drop Alice's job: %v", err)
	}
	p.runAll(t)

	if got := p.receipt(t, id).Status; got != "queued" {
		t.Errorf("Alice's receipt status = %q, want it untouched", got)
	}
	if got := len(p.azure.received()); got != 0 {
		t.Errorf("Azure received %d submissions for a job that owns nothing", got)
	}
}

func TestPipeline_DeletedReceiptJobSucceedsWithoutWork(t *testing.T) {
	p := newPipeline(t, 500)
	id := p.upload(t, p.alice, photo(t, 1))[0]
	if _, err := p.pool.Exec(t.Context(), "DELETE FROM receipts WHERE id = $1", id); err != nil {
		t.Fatalf("delete receipt: %v", err)
	}
	p.runAll(t)
	if n := p.count(t, "SELECT count(*) FROM jobs WHERE status = 'succeeded'"); n != 1 {
		t.Errorf("%d succeeded jobs, want 1", n)
	}
	if got := len(p.azure.received()); got != 0 {
		t.Errorf("Azure received %d submissions for a deleted receipt", got)
	}
}
