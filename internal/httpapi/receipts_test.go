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
	"slices"
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
	server := httpapi.New(":0", logger, http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{}, nil, nil, nil, nil, service)
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
	// The fixture's canonical store exists before extraction, so a seeded
	// receipt that prints it links by name and the test sees only the
	// flags it asked for: a name nothing matches would raise new_store on
	// top of them.
	if result.StoreName.Found && result.StoreName.Value == "Synthetic Market" {
		if _, err := s.q.UpsertExtractedStore(s.t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user, Name: "Synthetic Market", NormalizedName: "synthetic market", Now: s.now}); err != nil {
			s.t.Fatal(err)
		}
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

func assertBimChain(t *testing.T, chain *httpapi.ReceiptChain) {
	t.Helper()
	if chain == nil {
		t.Fatal("chain is nil")
	}
	if chain.Id != "bim" || chain.Name != "BİM" || chain.PortalUrl != "https://arsiv.bim.com.tr/" || chain.Captcha != httpapi.ReceiptChainCaptchaNone {
		t.Fatalf("chain identity=%+v", chain)
	}
	want := []struct{ name, description, value string }{
		{"belgeno", "Invoice number, ETTN or barcode/reference", "BIM2026001234"},
		{"tarih", "Invoice date", "05-03-2026"},
		{"tutar", "Total paid", "129.40"},
	}
	if len(chain.Fields) != len(want) {
		t.Fatalf("fields=%+v, want %d", chain.Fields, len(want))
	}
	for i, field := range want {
		got := chain.Fields[i]
		if got.Name != field.name || got.Description != field.description || got.Value == nil || *got.Value != field.value {
			t.Errorf("field %d=%+v, want %+v", i, got, field)
		}
	}
}

func TestReceipts_ChainFollowsSellerTaxNumber(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	updated := decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Chain receipt","currency":"TRY","purchasedOn":"2026-03-05","total":"129.40","invoiceNumber":"BIM2026001234","sellerTaxNumber":"1750051846"}`), 200)
	assertBimChain(t, updated.Chain)

	got := decodeReceipt(t, s.call(s.alice, "GET", base, ""), 200)
	assertBimChain(t, got.Chain)

	changed := decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Chain receipt","currency":"TRY","purchasedOn":"2026-03-05","total":"129.40","invoiceNumber":"BIM2026001234","sellerTaxNumber":"9999999999"}`), 200)
	if changed.Chain != nil {
		t.Errorf("chain for an unknown VKN=%+v, want nil", changed.Chain)
	}
}

func TestReceipts_IsolatesEveryOperation(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	// Make the receipt chain-bearing as the owner before the foreign-user
	// matrix, so every 404 below covers a receipt that does carry chain data.
	decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Chain receipt","currency":"TRY","purchasedOn":"2026-03-05","total":"129.40","invoiceNumber":"BIM2026001234","sellerTaxNumber":"1750051846"}`), 200)
	assertBimChain(t, decodeReceipt(t, s.call(s.alice, "GET", base, ""), 200).Chain)
	imageID := s.image(id, true)
	d, err := s.service.Get(t.Context(), s.alice, id)
	if err != nil {
		t.Fatal(err)
	}
	lineID := d.Lines[0].Item.ID
	for _, tc := range []struct{ method, path, body string }{
		{"GET", base, ""}, {"PUT", base, `{"title":"Changed","currency":"TRY","total":"10.00","invoiceNumber":"T112026000000000","ettn":"1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d","sellerTaxNumber":"1234567890"}`}, {"DELETE", base, ""}, {"POST", base + "/reprocess", ""}, {"GET", base + "/images/" + imageID.String(), ""}, {"GET", base + "/images/" + imageID.String() + "?variant=original", ""}, {"POST", base + "/line-items", `{"name":"Milk","lineTotal":"3.00"}`}, {"PUT", base + "/line-items/" + lineID.String(), `{"name":"Milk","lineTotal":"3.00"}`}, {"DELETE", base + "/line-items/" + lineID.String(), ""},
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

func TestReceipts_IdentityFieldsNormaliseAndClear(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	typed := decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Corrected receipt","currency":"TRY","invoiceNumber":" t112026000000000 ","ettn":"1A2B3C4D5E6F4A7B8C9D0E1F2A3B4C5D","sellerTaxNumber":" 1234567890 "}`), 200)
	if typed.InvoiceNumber == nil || *typed.InvoiceNumber != "T112026000000000" {
		t.Fatalf("invoice number=%v", typed.InvoiceNumber)
	}
	if typed.Ettn == nil || *typed.Ettn != "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d" {
		t.Fatalf("ettn=%v", typed.Ettn)
	}
	if typed.SellerTaxNumber == nil || *typed.SellerTaxNumber != "1234567890" {
		t.Fatalf("seller tax number=%v", typed.SellerTaxNumber)
	}
	// The branch reads like the identity: trimmed on the way in, and a
	// later update that omits it clears it.
	branch := decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Corrected receipt","currency":"TRY","branchName":" ANKARA ÇANKAYA "}`), 200)
	if branch.BranchName == nil || *branch.BranchName != "ANKARA ÇANKAYA" {
		t.Fatalf("branch name=%v", branch.BranchName)
	}
	branchCleared := decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Corrected receipt","currency":"TRY","branchName":null}`), 200)
	if branchCleared.BranchName != nil {
		t.Fatalf("cleared branch name=%v", *branchCleared.BranchName)
	}
	// The edit is audited with the identity before and after it.
	var audited int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE user_id=$1 AND entity_id=$2 AND detail->'before'->'Receipt'->>'InvoiceNumber' IS NULL AND detail->'after'->'Receipt'->>'InvoiceNumber' = $3", s.alice, id, "T112026000000000").Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("audits carrying the identity change=%d", audited)
	}
	cleared := decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Corrected receipt","currency":"TRY","invoiceNumber":"","ettn":"","sellerTaxNumber":""}`), 200)
	if cleared.InvoiceNumber != nil || cleared.Ettn != nil || cleared.SellerTaxNumber != nil {
		t.Fatalf("cleared identity=%v %v %v", cleared.InvoiceNumber, cleared.Ettn, cleared.SellerTaxNumber)
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

func TestReceipts_FiltersByCategoryAndBreakdownGroup(t *testing.T) {
	s := newReceiptServer(t)
	insert := func(query string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.pool.QueryRow(t.Context(), query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}

	dairy := insert(`INSERT INTO categories (user_id, name) VALUES ($1, 'QA Dairy') RETURNING id`, s.alice)
	bakery := insert(`INSERT INTO categories (user_id, name) VALUES ($1, 'QA Bakery') RETURNING id`, s.alice)
	deli := insert(`INSERT INTO categories (user_id, name) VALUES ($1, 'QA Deli') RETURNING id`, s.alice)
	bobCategory := insert(`INSERT INTO categories (user_id, name) VALUES ($1, 'Bob QA Dairy') RETURNING id`, s.bob)
	milk := insert(`INSERT INTO products (user_id, canonical_name, category_id) VALUES ($1, 'Milk', $2) RETURNING id`, s.alice, dairy)
	bread := insert(`INSERT INTO products (user_id, canonical_name, category_id) VALUES ($1, 'Bread', $2) RETURNING id`, s.alice, bakery)
	cheese := insert(`INSERT INTO products (user_id, canonical_name, category_id) VALUES ($1, 'Cheese', $2) RETURNING id`, s.alice, deli)
	generic := insert(`INSERT INTO products (user_id, canonical_name) VALUES ($1, 'Generic') RETURNING id`, s.alice)
	bobProduct := insert(`INSERT INTO products (user_id, canonical_name, category_id) VALUES ($1, 'Bob Milk', $2) RETURNING id`, s.bob, bobCategory)
	store := insert(`INSERT INTO stores (user_id, name, normalized_name) VALUES ($1, 'Market', 'market') RETURNING id`, s.alice)

	receipt := func(user uuid.UUID, date, total string, storeID any) uuid.UUID {
		t.Helper()
		return insert(`INSERT INTO receipts (user_id, title, purchased_on, total, currency, status, store_id)
			VALUES ($1, 'Synthetic receipt', $2, $3, 'TRY', 'ready', $4::uuid) RETURNING id`, user, date, total, storeID)
	}
	line := func(user, receiptID uuid.UUID, position int, total string, productID any) {
		t.Helper()
		status := "confirmed"
		if productID == nil {
			status = "unmatched"
		}
		exec(`INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, line_total, product_id, match_status)
			VALUES ($1, $2, $3, 'ITEM', 'Item', $4, $5::uuid, $6)`, user, receiptID, position, total, productID, status)
	}

	dairyReceipt := receipt(s.alice, "2026-03-10", "100.00", store)
	line(s.alice, dairyReceipt, 0, "100.00", milk)
	bakeryReceipt := receipt(s.alice, "2026-03-11", "50.00", store)
	line(s.alice, bakeryReceipt, 0, "50.00", bread)
	discountReceipt := receipt(s.alice, "2026-03-12", "90.00", store)
	line(s.alice, discountReceipt, 0, "100.00", cheese)
	line(s.alice, discountReceipt, 1, "-10.00", nil)
	uncategorisedReceipt := receipt(s.alice, "2026-03-13", "40.00", store)
	line(s.alice, uncategorisedReceipt, 0, "40.00", generic)
	unallocatedReceipt := receipt(s.alice, "2026-03-14", "60.00", store)
	noStoreReceipt := receipt(s.alice, "2026-03-15", "30.00", nil)
	oldDairyReceipt := receipt(s.alice, "2026-02-10", "10.00", store)
	line(s.alice, oldDairyReceipt, 0, "10.00", milk)
	bobReceipt := receipt(s.bob, "2026-03-10", "20.00", nil)
	line(s.bob, bobReceipt, 0, "20.00", bobProduct)

	listIDs := func(user uuid.UUID, path string) []uuid.UUID {
		t.Helper()
		r := s.call(user, "GET", path, "")
		if r.Code != 200 {
			t.Fatalf("GET %s status=%d: %s", path, r.Code, r.Body.String())
		}
		var page httpapi.ReceiptPage
		if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		ids := make([]uuid.UUID, 0, len(page.Items))
		for _, item := range page.Items {
			ids = append(ids, item.Id)
		}
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
		return ids
	}
	assertIDs := func(name string, got, want []uuid.UUID) {
		t.Helper()
		slices.SortFunc(want, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s (-want +got):\n%s", name, diff)
		}
	}

	assertIDs("category", listIDs(s.alice, "/receipts?month=2026-03&categoryId="+dairy.String()), []uuid.UUID{dairyReceipt})
	assertIDs("second category", listIDs(s.alice, "/receipts?month=2026-03&categoryId="+bakery.String()), []uuid.UUID{bakeryReceipt})
	assertIDs("discounts", listIDs(s.alice, "/receipts?month=2026-03&group=discounts"), []uuid.UUID{discountReceipt})
	assertIDs("uncategorised", listIDs(s.alice, "/receipts?month=2026-03&group=uncategorised"), []uuid.UUID{uncategorisedReceipt})
	assertIDs("unallocated", listIDs(s.alice, "/receipts?month=2026-03&group=unallocated"), []uuid.UUID{unallocatedReceipt, noStoreReceipt})
	assertIDs("no-store", listIDs(s.alice, "/receipts?month=2026-03&group=no-store"), []uuid.UUID{noStoreReceipt})

	// A cursor from one group must not be usable with another group's filters.
	first := s.call(s.alice, "GET", "/receipts?limit=1&group=unallocated", "")
	var page httpapi.ReceiptPage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil {
		t.Fatal("expected a cursor for the unallocated group")
	}
	r := s.call(s.alice, "GET", "/receipts?limit=1&group=no-store&cursor="+url.QueryEscape(*page.NextCursor), "")
	if r.Code != 400 {
		t.Fatalf("cross-filter cursor status=%d want 400: %s", r.Code, r.Body.String())
	}

	// The new filters never expose another user's receipts.
	assertIDs("isolation by category", listIDs(s.bob, "/receipts?month=2026-03&categoryId="+dairy.String()), []uuid.UUID{})
	assertIDs("own category", listIDs(s.bob, "/receipts?month=2026-03&categoryId="+bobCategory.String()), []uuid.UUID{bobReceipt})
	assertIDs("isolation by group", listIDs(s.bob, "/receipts?month=2026-03&group=discounts"), []uuid.UUID{})
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
		{"GET", "/receipts?group=unknown", "", 400},
		{"GET", "/receipts?categoryId=not-a-uuid", "", 400},
		{"GET", "/receipts?cursor=invalid", "", 400},
		{"PUT", base, `{"title":"","currency":"TRY"}`, 400},
		{"PUT", base, `{"title":"A","currency":"try"}`, 400},
		{"PUT", base, `{"title":"A","currency":"TRY","total":"abc"}`, 400},
		{"PUT", base, `{"title":"A","currency":"TRY","invoiceNumber":"T-1120260000"}`, 400},
		{"PUT", base, `{"title":"A","currency":"TRY","invoiceNumber":"T` + strings.Repeat("1", 33) + `"}`, 400},
		{"PUT", base, `{"title":"A","currency":"TRY","sellerTaxNumber":"123456789"}`, 400},
		{"PUT", base, `{"title":"A","currency":"TRY","sellerTaxNumber":"12345678AB"}`, 400},
		{"PUT", base, `{"title":"A","currency":"TRY","ettn":"not-a-uuid"}`, 400},
		{"PUT", base, `{"title":"A","currency":"TRY","ettn":"` + strings.Repeat("a", 31) + `"}`, 400},
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
	// One identity rejection is inspected fully: the problem names the field.
	r := s.call(s.alice, "PUT", base, `{"title":"A","currency":"TRY","sellerTaxNumber":"123456789"}`)
	if r.Code != 400 {
		t.Fatalf("tax status=%d: %s", r.Code, r.Body.String())
	}
	var problem httpapi.Problem
	if err := json.Unmarshal(r.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Errors == nil || len(*problem.Errors) != 1 || (*problem.Errors)[0].Field != "sellerTaxNumber" {
		t.Fatalf("field errors=%+v", problem.Errors)
	}
	queued := uuid.New()
	if err := s.q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: queued, UserID: s.alice, Title: "Waiting", Currency: "TRY", Source: "web", Now: s.now}); err != nil {
		t.Fatal(err)
	}
	if r := s.call(s.alice, "POST", "/receipts/"+queued.String()+"/reprocess", ""); r.Code != 409 {
		t.Fatalf("missing extraction=%d: %s", r.Code, r.Body.String())
	}
}

// wantFieldProblem checks that a 400 problem names exactly one field, with
// the message a person can act on.
func wantFieldProblem(t *testing.T, r *httptest.ResponseRecorder, field, message string) {
	t.Helper()
	if r.Code != 400 {
		t.Fatalf("status=%d want 400: %s", r.Code, r.Body.String())
	}
	problem := decodeProblem(t, r)
	if problem.Errors == nil || len(*problem.Errors) != 1 {
		t.Fatalf("problem has no single field error: %s", r.Body.String())
	}
	want := []httpapi.FieldProblem{{Field: field, Message: message}}
	if diff := cmp.Diff(want, *problem.Errors); diff != "" {
		t.Errorf("field errors mismatch (-want +got):\n%s", diff)
	}
}

func TestReceipts_AcceptFlexibleDecimalInput(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	updated := decodeReceipt(t, s.call(s.alice, "PUT", base, `{"title":"Flexible forms","currency":"TRY","subtotal":"84,5","taxTotal":"10.","total":"351,9"}`), 200)
	if updated.Subtotal == nil || *updated.Subtotal != "84.50" || updated.TaxTotal == nil || *updated.TaxTotal != "10.00" || updated.Total == nil || *updated.Total != "351.90" {
		t.Fatalf("updated=%+v", updated)
	}
	added := decodeReceipt(t, s.call(s.alice, "POST", base+"/line-items", `{"name":"SYNTH","quantity":"1,25","unitPrice":"5,5","lineTotal":"6,88"}`), 201)
	if len(added.LineItems) != 2 {
		t.Fatalf("lines=%d", len(added.LineItems))
	}
	line := added.LineItems[1]
	if line.Quantity == nil || *line.Quantity != "1.250" || line.UnitPrice == nil || *line.UnitPrice != "5.50" || line.LineTotal == nil || string(*line.LineTotal) != "6.88" {
		t.Fatalf("added line=%+v", line)
	}
}

func TestReceipts_RejectedDecimalsNameTheField(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	money := "enter a number with up to two decimal places, using . or , — such as 351.90"
	quantity := "enter a number with up to three decimal places, using . or , — such as 1.250"
	for _, tc := range []struct {
		name, method, path, body, field, message string
	}{
		{"text amount", "PUT", base, `{"title":"A","currency":"TRY","total":"abc"}`, "total", money},
		{"grouped thousands", "PUT", base, `{"title":"A","currency":"TRY","total":"1.234,56"}`, "total", money},
		{"three decimals", "PUT", base, `{"title":"A","currency":"TRY","total":"0,114"}`, "total", money},
		{"blank required line total", "POST", base + "/line-items", `{"name":"A","lineTotal":""}`, "lineTotal", money},
		{"four decimal quantity", "POST", base + "/line-items", `{"name":"A","quantity":"1.0001","lineTotal":"10.00"}`, "quantity", quantity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantFieldProblem(t, s.call(s.alice, tc.method, tc.path, tc.body), tc.field, tc.message)
		})
	}
}

func TestReceipts_NameEveryRejectedField(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	positive := "must be positive with at most three decimal places"
	for _, tc := range []struct {
		name, method, path, body, field, message string
	}{
		{"blank title", "PUT", base, `{"title":"","currency":"TRY"}`, "title", "must be 1 to 200 characters"},
		{"lowercase currency", "PUT", base, `{"title":"A","currency":"try"}`, "currency", "must be a three-letter currency code, such as TRY"},
		{"punctuation-only store", "PUT", base, `{"title":"A","currency":"TRY","storeName":"!!!"}`, "storeName", "must be 1 to 200 characters and not only punctuation"},
		{"partial time", "PUT", base, `{"title":"A","currency":"TRY","purchasedTime":"8:42"}`, "purchasedTime", "must be a time of day as HH:MM, such as 18:42"},
		{"negative quantity", "POST", base + "/line-items", `{"name":"A","quantity":"-2","lineTotal":"10.00"}`, "quantity", positive},
		{"zero quantity", "POST", base + "/line-items", `{"name":"A","quantity":"0","lineTotal":"10.00"}`, "quantity", positive},
		{"long product code", "POST", base + "/line-items", `{"name":"A","lineTotal":"10.00","productCode":"` + strings.Repeat("1", 65) + `"}`, "productCode", "must be 1 to 64 characters"},
		{"malformed month", "GET", "/receipts?month=2026-13", "", "month", "must be written as YYYY-MM"},
		{"empty month", "GET", "/receipts?month=", "", "month", "must be written as YYYY-MM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantFieldProblem(t, s.call(s.alice, tc.method, tc.path, tc.body), tc.field, tc.message)
		})
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

func TestReceipts_HugeDerivedFlagAmountsStayInContract(t *testing.T) {
	s := newReceiptServer(t)
	huge := decimal.RequireFromString("9999999999.99")
	mismatch := cleanReceipt()
	mismatch.Total = receiptField(decimal.RequireFromString("0.01"))
	mismatch.Lines = []extract.Line{
		{Name: receiptField("BREAD"), Quantity: receiptField(decimal.New(1, 0)), UnitPrice: receiptField(huge), Total: receiptField(huge)},
		{Name: receiptField("RICE"), Quantity: receiptField(decimal.New(1, 0)), UnitPrice: receiptField(huge), Total: receiptField(huge)},
	}
	mismatchID := s.seed(s.alice, mismatch)

	math := cleanReceipt()
	math.Lines = []extract.Line{{Name: receiptField("BREAD"), Quantity: receiptField(decimal.RequireFromString("999999999.999")), UnitPrice: receiptField(huge), Total: receiptField(decimal.New(100, -2))}}
	mathID := s.seed(s.alice, math)

	receipt := decodeReceipt(t, s.call(s.alice, http.MethodGet, "/receipts/"+mismatchID.String(), ""), 200)
	var sums, differences int
	for _, flag := range receipt.Flags {
		if kind, err := flag.Discriminator(); err != nil || kind != "sum_mismatch" {
			continue
		}
		sum, err := flag.AsSumMismatchFlag()
		if err != nil {
			t.Fatal(err)
		}
		if sum.Detail.LineSum != "19999999999.98" {
			t.Errorf("lineSum = %q, want the sum of both lines", sum.Detail.LineSum)
		}
		if sum.Detail.Difference != "-19999999999.97" {
			t.Errorf("difference = %q, want total minus line sum", sum.Detail.Difference)
		}
		sums++
		differences++
	}
	if sums != 1 || differences != 1 {
		t.Fatalf("sum_mismatch flags = %+v", receipt.Flags)
	}

	receipt = decodeReceipt(t, s.call(s.alice, http.MethodGet, "/receipts/"+mathID.String(), ""), 200)
	var expectations int
	for _, flag := range receipt.Flags {
		if kind, err := flag.Discriminator(); err != nil || kind != "line_math" {
			continue
		}
		mathFlag, err := flag.AsLineMathFlag()
		if err != nil {
			t.Fatal(err)
		}
		if mathFlag.Detail.ExpectedTotal != "9999999999980000000.00" {
			t.Errorf("expectedTotal = %q, want quantity times unit price", mathFlag.Detail.ExpectedTotal)
		}
		expectations++
	}
	if expectations != 1 {
		t.Fatalf("line_math flags = %+v", receipt.Flags)
	}

	// The flag list answers with the same shapes as the receipt detail.
	response := s.call(s.alice, http.MethodGet, "/review-flags", "")
	if response.Code != http.StatusOK {
		t.Fatalf("review list = %d: %s", response.Code, response.Body.String())
	}
	var document any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := loadContract(t).Components.Schemas["ReviewFlagPage"].Value.VisitJSON(document); err != nil {
		t.Fatalf("review flags violate contract: %v\n%s", err, response.Body.String())
	}
}
