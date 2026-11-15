package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type receiptImages struct {
	data    map[string][]byte
	failure error
	opened  int
	deleted []string
}

func (s *receiptImages) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) (string, error) {
	data, err := io.ReadAll(r)
	if err == nil {
		s.data[key] = data
	}
	return key, err
}

func (s *receiptImages) Open(_ context.Context, ref string) (io.ReadCloser, error) {
	s.opened++
	if s.failure != nil {
		return nil, s.failure
	}
	data, ok := s.data[ref]
	if !ok {
		return nil, apperr.NotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *receiptImages) Delete(_ context.Context, ref string) error {
	if s.failure != nil {
		return s.failure
	}
	if _, ok := s.data[ref]; !ok {
		return apperr.NotFound
	}
	delete(s.data, ref)
	s.deleted = append(s.deleted, ref)
	return nil
}

type receiptParser struct{ calls int }

func (p *receiptParser) Parse(raw json.RawMessage) (extract.Result, error) {
	p.calls++
	var result extract.Result
	err := json.Unmarshal(raw, &result)
	return result, err
}

type receiptServer struct {
	t          *testing.T
	pool       *pgxpool.Pool
	q          *sqlc.Queries
	service    *receipts.Service
	http       *httpapi.Server
	sessions   *scs.SessionManager
	alice, bob uuid.UUID
	images     *receiptImages
	parser     *receiptParser
	now        time.Time
}

func newReceiptServer(t *testing.T) *receiptServer {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: "alice@example.com"}
	users := auth.New(pool, cfg, nil, time.Now)
	alice := testutil.NewUser(t, users).ID
	cfg.DevLoginEmail = "bob@example.com"
	bob := testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	sessions, stop := auth.NewSessions(pool, false)
	t.Cleanup(stop)
	images := &receiptImages{data: map[string][]byte{}}
	parser := &receiptParser{}
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	service := receipts.New(pool, func() time.Time { return now }, receipts.Options{Images: images, Parsers: map[string]receipts.ResultParser{extract.ProviderFake: parser}})
	server := httpapi.New(":0", logger, http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{}, nil, nil, nil, service)
	return &receiptServer{t: t, pool: pool, q: sqlc.New(pool), service: service, http: server, sessions: sessions, alice: alice, bob: bob, images: images, parser: parser, now: now}
}

func receiptField[T any](value T) extract.Field[T] {
	return extract.Field[T]{Value: value, Confidence: .99, Found: true}
}

func cleanReceipt() extract.Result {
	return extract.Result{StoreName: receiptField("Synthetic Market"), Date: receiptField(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)), Total: receiptField(decimal.New(10, 0)), Lines: []extract.Line{{Name: receiptField("BREAD"), Quantity: receiptField(decimal.New(1, 0)), UnitPrice: receiptField(decimal.New(10, 0)), Total: receiptField(decimal.New(10, 0)), Confidence: .99}}}
}

func (s *receiptServer) seed(user uuid.UUID, result extract.Result) uuid.UUID {
	s.t.Helper()
	id := uuid.New()
	if err := s.q.CreateQueuedReceipt(s.t.Context(), sqlc.CreateQueuedReceiptParams{ID: id, UserID: user, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: s.now}); err != nil {
		s.t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.q.CreateExtraction(s.t.Context(), sqlc.CreateExtractionParams{UserID: user, ReceiptID: id, Provider: "fake", Model: "synthetic", Raw: raw, Now: s.now}); err != nil {
		s.t.Fatal(err)
	}
	if err := s.service.ApplyResult(s.t.Context(), user, id, result); err != nil {
		s.t.Fatal(err)
	}
	return id
}

func (s *receiptServer) call(user uuid.UUID, method, path, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	request := testutil.Request(s.t, s.sessions, user, method, "/api/v1"+path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	s.http.Handler().ServeHTTP(response, request)
	return response
}

func (s *receiptServer) image(receiptID uuid.UUID, original bool) uuid.UUID {
	s.t.Helper()
	id := uuid.New()
	ref := receiptID.String() + "/" + id.String() + ".jpg"
	s.images.data[ref] = []byte("display bytes")
	p := sqlc.CreateReceiptImageParams{ID: id, UserID: s.alice, ReceiptID: receiptID, Sha256: nil, ContentType: "image/jpeg", SizeBytes: 13, StorageBackend: "local", StorageRef: ref, Now: s.now}
	sum := sha256.Sum256([]byte(ref))
	p.Sha256 = sum[:]
	if original {
		originalRef := ref + ".heic"
		s.images.data[originalRef] = []byte("original bytes")
		kind := "image/heic"
		size := int64(14)
		p.OriginalRef = &originalRef
		p.OriginalContentType = &kind
		p.OriginalSizeBytes = &size
	}
	if err := s.q.CreateReceiptImage(s.t.Context(), p); err != nil {
		s.t.Fatal(err)
	}
	return id
}

func decodeReceipt(t *testing.T, r *httptest.ResponseRecorder, status int) httpapi.Receipt {
	t.Helper()
	if r.Code != status {
		t.Fatalf("status=%d want %d: %s", r.Code, status, r.Body.String())
	}
	var receipt httpapi.Receipt
	if err := json.Unmarshal(r.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	var document interface{}
	if err := json.Unmarshal(r.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := loadContract(t).Components.Schemas["Receipt"].Value.VisitJSON(document); err != nil {
		t.Fatalf("receipt violates contract: %v", err)
	}
	return receipt
}

func TestReceipts_IsolatesEveryOperation(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	imageID := s.image(id, true)
	d, err := s.service.Get(t.Context(), s.alice, id)
	if err != nil {
		t.Fatal(err)
	}
	lineID := d.Lines[0].Item.ID
	base := "/receipts/" + id.String()
	for _, tc := range []struct{ method, path, body string }{
		{"GET", base, ""}, {"PUT", base, `{"title":"Changed","currency":"TRY","total":"10.00"}`}, {"DELETE", base, ""}, {"POST", base + "/reprocess", ""}, {"GET", base + "/images/" + imageID.String(), ""}, {"GET", base + "/images/" + imageID.String() + "?variant=original", ""}, {"POST", base + "/line-items", `{"name":"Milk","lineTotal":"3.00"}`}, {"PUT", base + "/line-items/" + lineID.String(), `{"name":"Milk","lineTotal":"3.00"}`}, {"DELETE", base + "/line-items/" + lineID.String(), ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := s.call(s.bob, tc.method, tc.path, tc.body)
			if r.Code != 404 {
				t.Fatalf("foreign status=%d: %s", r.Code, r.Body.String())
			}
		})
	}
	if s.images.opened != 0 || len(s.images.deleted) != 0 {
		t.Fatal("foreign request touched image storage")
	}
	r := s.call(s.bob, "GET", "/receipts?ids="+id.String(), "")
	if r.Code != 200 {
		t.Fatalf("list=%d %s", r.Code, r.Body.String())
	}
	var page httpapi.ReceiptPage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatal("list leaked a foreign receipt")
	}
}

func TestReceipts_ReadEditAndReprocess(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	initial := decodeReceipt(t, s.call(s.alice, "GET", base, ""), 200)
	if initial.Status != "ready" || initial.Total == nil || *initial.Total != "10.00" {
		t.Fatalf("receipt=%+v", initial)
	}
	updated := decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Corrected receipt","currency":"EUR","storeName":"Other Store","purchasedOn":"2026-10-31","purchasedTime":"18:42","subtotal":"9.00","taxTotal":"1.00","total":"10.00"}`), 200)
	if updated.Title != "Corrected receipt" || updated.Currency != "EUR" || updated.PurchasedTime == nil || *updated.PurchasedTime != "18:42" || updated.Store == nil || updated.Store.Name != "Other Store" {
		t.Fatalf("update=%+v", updated)
	}
	first := decodeReceipt(t, s.call(s.alice, "POST", base+"/reprocess", ""), 200)
	second := decodeReceipt(t, s.call(s.alice, "POST", base+"/reprocess", ""), 200)
	if s.parser.calls != 2 {
		t.Fatalf("parse calls=%d", s.parser.calls)
	}
	if diff := cmp.Diff(receiptJSON(t, first), receiptJSON(t, second)); diff != "" {
		t.Fatalf("reprocess not idempotent: %s", diff)
	}
	if first.Currency != "EUR" || first.PurchasedOn.String() != "2026-11-15" {
		t.Fatalf("stored extraction fields=%+v", first)
	}
	var auditCount int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE user_id=$1 AND entity_id=$2 AND detail ? 'before' AND detail ? 'after'", s.alice, id).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 {
		t.Fatalf("audits=%d", auditCount)
	}
}

func TestReceipts_LineEditsRerunRulesAndMatching(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	initial := decodeReceipt(t, s.call(s.alice, "GET", base, ""), 200)
	lineID := initial.LineItems[0].Id
	added := decodeReceipt(t, s.call(s.alice, "POST", base+"/line-items", `{"name":"Discount","lineTotal":"-2.00"}`), 201)
	if len(added.LineItems) != 2 || added.LineItems[1].MatchStatus != "unmatched" || added.LineItems[1].RawName != "Discount" {
		t.Fatalf("added=%+v", added)
	}
	changed := decodeReceipt(t, s.call(s.alice, "PUT", base+"/line-items/"+lineID.String(), `{"name":"Rice 1 kg","quantity":"2","unitPrice":"5.00","lineTotal":"10.02"}`), 200)
	if changed.LineItems[0].Product == nil || changed.LineItems[0].Product.Id == initial.LineItems[0].Product.Id {
		t.Fatal("changed name was not rematched")
	}
	if changed.LineItems[0].RawName != "BREAD" {
		t.Fatal("editing destroyed extraction text")
	}
	var hasMath bool
	for _, flag := range changed.Flags {
		kind, err := flag.Discriminator()
		if err != nil {
			t.Fatal(err)
		}
		hasMath = hasMath || kind == "line_math"
	}
	if !hasMath {
		t.Fatal("line math was not re-evaluated")
	}
	fixed := decodeReceipt(t, s.call(s.alice, "PUT", base+"/line-items/"+lineID.String(), `{"name":"Rice 1 kg","quantity":"2","unitPrice":"5.00","lineTotal":"10.00"}`), 200)
	for _, flag := range fixed.Flags {
		kind, err := flag.Discriminator()
		if err != nil {
			t.Fatal(err)
		}
		if kind == "line_math" {
			t.Fatal("old math flag remains open")
		}
	}
	discountID := added.LineItems[1].Id
	deleted := decodeReceipt(t, s.call(s.alice, "DELETE", base+"/line-items/"+discountID.String(), ""), 200)
	if len(deleted.LineItems) != 1 || deleted.Status != "ready" {
		t.Fatalf("after deletion=%+v", deleted)
	}
	// Returning to a previous bad value must raise a new flag, rather than reuse a superseded one.
	changed = decodeReceipt(t, s.call(s.alice, "PUT", base+"/line-items/"+lineID.String(), `{"name":"Rice 1 kg","quantity":"2","unitPrice":"5.00","lineTotal":"10.02"}`), 200)
	if changed.OpenFlagCount != 1 {
		t.Fatalf("repeated cause flags=%d", changed.OpenFlagCount)
	}
}

func TestReceipts_PrivateImagesAndDeletion(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	imageID := s.image(id, true)
	base := "/receipts/" + id.String()
	imagePath := base + "/images/" + imageID.String()
	for _, tc := range []struct{ suffix, want, kind string }{{"", "display bytes", "image/jpeg"}, {"?variant=original", "original bytes", "image/heic"}} {
		r := s.call(s.alice, "GET", imagePath+tc.suffix, "")
		if r.Code != 200 || r.Body.String() != tc.want || r.Header().Get("Content-Type") != tc.kind || !strings.HasPrefix(r.Header().Get("Cache-Control"), "private") {
			t.Fatalf("image=%d %q %v", r.Code, r.Body.String(), r.Header())
		}
	}
	if _, err := jobs.Enqueue(t.Context(), s.pool, jobs.KindExtractReceipt, jobs.ExtractReceipt{UserID: s.alice, ReceiptID: id}, s.now); err != nil {
		t.Fatal(err)
	}
	s.images.failure = errors.New("synthetic storage outage")
	if r := s.call(s.alice, "GET", imagePath, ""); r.Code != 503 {
		t.Fatalf("storage read=%d", r.Code)
	}
	if r := s.call(s.alice, "DELETE", base, ""); r.Code != 503 {
		t.Fatalf("storage delete=%d", r.Code)
	}
	if _, err := s.service.Get(t.Context(), s.alice, id); err != nil {
		t.Fatal("failed delete removed receipt")
	}
	s.images.failure = nil
	if r := s.call(s.alice, "DELETE", base, ""); r.Code != 204 {
		t.Fatalf("delete=%d %s", r.Code, r.Body.String())
	}
	if len(s.images.data) != 0 || len(s.images.deleted) != 2 {
		t.Fatalf("stored objects=%v", s.images.data)
	}
	for _, table := range []string{"receipts", "line_items", "review_flags", "receipt_images", "extractions"} {
		var count int
		query := "SELECT count(*) FROM " + table + " WHERE user_id=$1"
		if err := s.pool.QueryRow(t.Context(), query, s.alice).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s still has %d rows", table, count)
		}
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("jobs=%d", count)
	}
}

func TestReceipts_PagesAndCombinesFilters(t *testing.T) {
	s := newReceiptServer(t)
	a := cleanReceipt()
	a.Date = extract.Field[time.Time]{}
	first := s.seed(s.alice, a)
	second := s.seed(s.alice, cleanReceipt())
	old := cleanReceipt()
	old.Date = receiptField(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	third := s.seed(s.alice, old)
	s.seed(s.bob, cleanReceipt())
	var ids []uuid.UUID
	cursor := ""
	for range 3 {
		path := "/receipts?limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		r := s.call(s.alice, "GET", path, "")
		if r.Code != 200 {
			t.Fatalf("page=%d %s", r.Code, r.Body.String())
		}
		var page httpapi.ReceiptPage
		if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("page=%+v", page)
		}
		ids = append(ids, page.Items[0].Id)
		if page.NextCursor == nil {
			cursor = ""
		} else {
			cursor = *page.NextCursor
		}
	}
	if diff := cmp.Diff([]uuid.UUID{first, second, third}, ids); diff != "" {
		t.Fatal(diff)
	}
	d, err := s.service.Get(t.Context(), s.alice, second)
	if err != nil {
		t.Fatal(err)
	}
	path := "/receipts?month=2026-11&status=ready&q=bread&storeId=" + d.Store.ID.String() + "&ids=" + second.String()
	r := s.call(s.alice, "GET", path, "")
	var page httpapi.ReceiptPage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || len(page.Items) != 1 || page.Items[0].Id != second {
		t.Fatalf("filters=%d %s", r.Code, r.Body.String())
	}
}

func TestReceipts_RejectsInvalidInputsAndMissingResources(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/receipts?limit=0", "", 400},
		{"GET", "/receipts?month=", "", 400},
		{"GET", "/receipts?status=", "", 400},
		{"PUT", base, `{"title":"A","currency":"TRY","purchasedTime":"8:42"}`, 400},
		{"POST", base + "/line-items", `{"name":"A","lineTotal":"10.00","productCode":"` + strings.Repeat("1", 65) + `"}`, 400},
		{"GET", "/receipts?limit=101", "", 400},
		{"GET", "/receipts?month=2026-13", "", 400},
		{"GET", "/receipts?status=unknown", "", 400},
		{"GET", "/receipts?cursor=invalid", "", 400},
		{"PUT", base, `{"title":"","currency":"TRY"}`, 400},
		{"PUT", base, `{"title":"A","currency":"try"}`, 400},
		{"PUT", base, `{"title":"A","currency":"TRY","total":"10.0"}`, 400},
		{"POST", base + "/line-items", `{"name":"A","lineTotal":"NaN"}`, 400},
		{"POST", base + "/line-items", `{"name":"A","quantity":"1.0001","lineTotal":"10.00"}`, 400},
		{"GET", base + "/images/" + uuid.NewString(), "", 404},
		{"GET", base + "/images/" + uuid.NewString() + "?variant=bad", "", 400},
		{"GET", "/receipts/" + uuid.NewString(), "", 404},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			r := s.call(s.alice, tc.method, tc.path, tc.body)
			if r.Code != tc.status {
				t.Fatalf("status=%d want %d: %s", r.Code, tc.status, r.Body.String())
			}
		})
	}
	queued := uuid.New()
	if err := s.q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: queued, UserID: s.alice, Title: "Waiting", Currency: "TRY", Source: "web", Now: s.now}); err != nil {
		t.Fatal(err)
	}
	if r := s.call(s.alice, "POST", "/receipts/"+queued.String()+"/reprocess", ""); r.Code != 409 {
		t.Fatalf("missing extraction=%d: %s", r.Code, r.Body.String())
	}
}

func TestReceipts_ReprocessPreservesOpenFlagIdentities(t *testing.T) {
	s := newReceiptServer(t)
	result := cleanReceipt()
	result.Total.Confidence = .7
	result.Lines[0].Confidence = .7
	result.Lines[0].Quantity = receiptField(decimal.New(2, 0))
	id := s.seed(s.alice, result)
	path := "/receipts/" + id.String() + "/reprocess"
	first := decodeReceipt(t, s.call(s.alice, "POST", path, ""), 200)
	second := decodeReceipt(t, s.call(s.alice, "POST", path, ""), 200)
	if len(first.Flags) != 3 {
		t.Fatalf("flags=%d", len(first.Flags))
	}
	if diff := cmp.Diff(receiptJSON(t, first), receiptJSON(t, second)); diff != "" {
		t.Fatalf("flag identities changed: %s", diff)
	}
}

func TestReceipts_CursorIsBoundToUserAndFilters(t *testing.T) {
	s := newReceiptServer(t)
	for range 3 {
		s.seed(s.alice, cleanReceipt())
	}
	r := s.call(s.alice, "GET", "/receipts?limit=1", "")
	var page httpapi.ReceiptPage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil {
		t.Fatal("missing cursor")
	}
	for _, user := range []uuid.UUID{s.alice, s.bob} {
		path := "/receipts?limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
		if user == s.alice {
			path += "&q=bread"
		}
		if response := s.call(user, "GET", path, ""); response.Code != 400 {
			t.Fatalf("changed scope=%d %s", response.Code, response.Body.String())
		}
	}
}

func TestReceipts_AuditFailureRollsBackUserEdit(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	if _, err := s.pool.Exec(t.Context(), "ALTER TABLE audit_events ADD CONSTRAINT synthetic_audit_failure CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	r := s.call(s.alice, "PUT", base, `{"title":"Must roll back","currency":"EUR","storeName":"New Store","total":"20.00"}`)
	if r.Code != 500 {
		t.Fatalf("failed audit=%d %s", r.Code, r.Body.String())
	}
	receipt := decodeReceipt(t, s.call(s.alice, "GET", base, ""), 200)
	if receipt.Title != "Synthetic receipt" || receipt.Currency != "TRY" || receipt.Store.Name != "Synthetic Market" || *receipt.Total != "10.00" {
		t.Fatalf("edit survived rollback: %+v", receipt)
	}
}

func TestReceipts_OriginalVariantRequiresConvertedUpload(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	imageID := s.image(id, false)
	path := "/receipts/" + id.String() + "/images/" + imageID.String() + "?variant=original"
	r := s.call(s.alice, "GET", path, "")
	if r.Code != 404 || s.images.opened != 0 {
		t.Fatalf("original=%d, opens=%d", r.Code, s.images.opened)
	}
}

func receiptJSON(t *testing.T, receipt httpapi.Receipt) string {
	t.Helper()
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
