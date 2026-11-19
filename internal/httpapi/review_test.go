package httpapi_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

func receiptFlag(t *testing.T, s *receiptServer, id uuid.UUID, kind string) sqlc.ReviewFlag {
	t.Helper()
	d, err := s.service.Get(t.Context(), s.alice, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range d.Flags {
		if flag.Kind == kind {
			return flag
		}
	}
	t.Fatalf("no %s flag in %+v", kind, d.Flags)
	return sqlc.ReviewFlag{}
}

func TestReview_ConfirmCorrectAndAcceptEveryAllowedValue(t *testing.T) {
	for _, tc := range []struct {
		name, kind, action, body string
		mutate                   func(*extract.Result)
	}{
		{"confirm store", "low_confidence", "confirm", `{"action":"confirm"}`, func(r *extract.Result) { r.StoreName.Confidence = .7 }},
		{"correct store", "low_confidence", "correct", `{"action":"correct","storeName":"Corrected Store"}`, func(r *extract.Result) { r.StoreName.Confidence = .7 }},
		{"confirm date", "low_confidence", "confirm", `{"action":"confirm"}`, func(r *extract.Result) { r.Date.Confidence = .7 }},
		{"correct date", "low_confidence", "correct", `{"action":"correct","purchasedOn":"2026-11-16"}`, func(r *extract.Result) { r.Date.Confidence = .7 }},
		{"confirm total", "low_confidence", "confirm", `{"action":"confirm"}`, func(r *extract.Result) { r.Total.Confidence = .7 }},
		{"correct total", "low_confidence", "correct", `{"action":"correct","total":"10.00"}`, func(r *extract.Result) { r.Total.Confidence = .7 }},
		{"correct total comma", "low_confidence", "correct", `{"action":"correct","total":"10,0"}`, func(r *extract.Result) { r.Total.Confidence = .7 }},
		{"confirm line", "low_confidence", "confirm", `{"action":"confirm"}`, func(r *extract.Result) { r.Lines[0].Confidence = .7 }},
		{"correct line", "low_confidence", "correct", `{"action":"correct","lineItem":{"name":"BREAD","quantity":"2","unitPrice":"5.00","lineTotal":"10.00"}}`, func(r *extract.Result) { r.Lines[0].Confidence = .7 }},
		{"correct line comma", "low_confidence", "correct", `{"action":"correct","lineItem":{"name":"BREAD","quantity":",5","unitPrice":"20,0","lineTotal":"10,0"}}`, func(r *extract.Result) { r.Lines[0].Confidence = .7 }},
		{"supply date", "missing_field", "correct", `{"action":"correct","purchasedOn":"2026-11-16"}`, func(r *extract.Result) { r.Date.Found = false }},
		{"accept missing date", "missing_field", "accept", `{"action":"accept"}`, func(r *extract.Result) { r.Date.Found = false }},
		{"supply total", "missing_field", "correct", `{"action":"correct","total":"10.00"}`, func(r *extract.Result) { r.Total.Found = false }},
		{"accept missing total", "missing_field", "accept", `{"action":"accept"}`, func(r *extract.Result) { r.Total.Found = false }},
		{"fix math", "line_math", "correct", `{"action":"correct","lineItem":{"name":"BREAD","quantity":"1","unitPrice":"10.00","lineTotal":"10.00"}}`, func(r *extract.Result) { r.Lines[0].Quantity = receiptField(decimal.New(2, 0)) }},
		{"accept math", "line_math", "accept", `{"action":"accept"}`, func(r *extract.Result) { r.Lines[0].Quantity = receiptField(decimal.New(2, 0)) }},
		{"accept sum", "sum_mismatch", "accept", `{"action":"accept"}`, func(r *extract.Result) { r.Total = receiptField(decimal.New(11, 0)) }},
		{"accept duplicate", "possible_duplicate", "accept", `{"action":"accept"}`, func(_ *extract.Result) {}},
		{"confirm new store", "new_store", "confirm", `{"action":"confirm"}`, func(r *extract.Result) { r.StoreName.Value = "Unmatched Market" }},
		{"correct new store", "new_store", "correct", `{"action":"correct","storeName":"Corrected Store"}`, func(r *extract.Result) { r.StoreName.Value = "Unmatched Market" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newReceiptServer(t)
			result := cleanReceipt()
			tc.mutate(&result)
			if tc.kind == "possible_duplicate" {
				s.seed(s.alice, cleanReceipt())
			}
			id := s.seed(s.alice, result)
			flag := receiptFlag(t, s, id, tc.kind)
			response := decodeReceipt(t, s.call(s.alice, "POST", "/review-flags/"+flag.ID.String()+"/resolve", tc.body), 200)
			if response.Status != "ready" || response.OpenFlagCount != 0 {
				t.Fatalf("resolution=%+v", response)
			}
			stored, err := s.q.GetOwnedReviewFlag(t.Context(), sqlc.GetOwnedReviewFlagParams{UserID: s.alice, FlagID: flag.ID})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"confirm": "confirmed", "correct": "corrected", "accept": "accepted"}[tc.action]
			if stored.Resolution == nil || *stored.Resolution != want || stored.ResolvedAt == nil || !stored.ResolvedAt.Equal(s.now) {
				t.Fatalf("stored resolution=%+v", stored)
			}
			switch tc.name {
			case "correct store", "correct new store":
				if response.Store == nil || response.Store.Name != "Corrected Store" {
					t.Fatal("store was not corrected")
				}
			case "correct date", "supply date":
				if response.PurchasedOn == nil || response.PurchasedOn.String() != "2026-11-16" {
					t.Fatal("date was not corrected")
				}
			case "correct line":
				if response.LineItems[0].Quantity == nil || *response.LineItems[0].Quantity != "2.000" || response.LineItems[0].Confidence != nil {
					t.Fatal("line was not corrected")
				}
			case "accept missing date":
				if response.PurchasedOn != nil {
					t.Fatal("accept invented a date")
				}
			case "accept missing total":
				if response.Total != nil {
					t.Fatal("accept invented a total")
				}
			}
			var count int
			if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE user_id=$1 AND entity='review_flag' AND entity_id=$2 AND action=$3 AND detail ? 'before' AND detail ? 'after'", s.alice, flag.ID, want).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("resolution audit rows=%d", count)
			}
		})
	}
}

func prepareMatchFlag(t *testing.T, s *receiptServer, kind string) (uuid.UUID, sqlc.ReviewFlag, []uuid.UUID) {
	t.Helper()
	storeID, err := s.q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: s.alice, Name: "Synthetic Market", NormalizedName: "synthetic market", Now: s.now})
	if err != nil {
		t.Fatal(err)
	}
	store := uuid.NullUUID{UUID: storeID, Valid: true}
	var ids []uuid.UUID
	for i, name := range []string{"MILK", "BREAD"} {
		p, err := s.q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: s.alice, CanonicalName: name, Now: s.now})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
		if i == 0 || kind == "uncertain_match" {
			if err := s.q.RememberReviewCode(t.Context(), sqlc.RememberReviewCodeParams{UserID: s.alice, ProductID: p.ID, Code: "1234567", Kind: "store", StoreID: store, Now: s.now}); err != nil {
				t.Fatal(err)
			}
		}
		if i == 1 && kind == "code_conflict" {
			if err := s.q.RememberReviewAlias(t.Context(), sqlc.RememberReviewAliasParams{UserID: s.alice, ProductID: p.ID, RawName: name, NormalizedName: "bread", Now: s.now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	result := cleanReceipt()
	result.Lines[0].ProductCode = receiptField("1234567")
	id := s.seed(s.alice, result)
	return id, receiptFlag(t, s, id, kind), ids
}

func TestReview_ProductChoicesTeachMatchingAndRejections(t *testing.T) {
	for _, kind := range []string{"uncertain_match", "code_conflict"} {
		for _, action := range []string{"same_product", "different_product"} {
			t.Run(kind+" "+action, func(t *testing.T) {
				s := newReceiptServer(t)
				id, flag, suggestions := prepareMatchFlag(t, s, kind)
				body := `{"action":"different_product","canonicalName":"Wholemeal loaf"}`
				var chosen uuid.UUID
				if action == "same_product" {
					product, err := s.q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: s.alice, CanonicalName: "Chosen product", Now: s.now})
					if err != nil {
						t.Fatal(err)
					}
					chosen = product.ID
					body = fmt.Sprintf(`{"action":"same_product","productId":%q}`, chosen.String())
				}
				response := decodeReceipt(t, s.call(s.alice, "POST", "/review-flags/"+flag.ID.String()+"/resolve", body), 200)
				if response.OpenFlagCount != 0 || response.Status != "ready" || response.LineItems[0].Product == nil || response.LineItems[0].MatchStatus != "confirmed" || response.LineItems[0].MatchMethod == nil || *response.LineItems[0].MatchMethod != "user" {
					t.Fatalf("product resolution=%+v", response)
				}
				if action == "different_product" {
					chosen = response.LineItems[0].Product.Id
					if response.LineItems[0].Product.CanonicalName != "Wholemeal loaf" {
						t.Fatal("canonical name was not applied")
					}
					var rejected int
					if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM match_rejections WHERE user_id=$1 AND product_id=ANY($2) AND normalized_name='bread'", s.alice, suggestions).Scan(&rejected); err != nil {
						t.Fatal(err)
					}
					if rejected != len(suggestions) {
						t.Fatalf("rejections=%d want %d", rejected, len(suggestions))
					}
				}
				if response.LineItems[0].Product.Id != chosen {
					t.Fatal("wrong product linked")
				}
				var aliasCount, codeCount int
				if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM product_aliases WHERE user_id=$1 AND product_id=$2 AND normalized_name='bread' AND store_id=$3", s.alice, chosen, response.Store.Id).Scan(&aliasCount); err != nil {
					t.Fatal(err)
				}
				if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM product_codes WHERE user_id=$1 AND product_id=$2 AND code='1234567' AND kind='store' AND store_id=$3", s.alice, chosen, response.Store.Id).Scan(&codeCount); err != nil {
					t.Fatal(err)
				}
				if aliasCount != 1 || codeCount != 1 {
					t.Fatalf("learned alias=%d code=%d", aliasCount, codeCount)
				}
				after := decodeReceipt(t, s.call(s.alice, "POST", "/receipts/"+id.String()+"/reprocess", ""), 200)
				if after.LineItems[0].Product == nil || after.LineItems[0].Product.Id != chosen || after.OpenFlagCount != 0 {
					t.Fatalf("learning did not survive reprocess: %+v", after)
				}
			})
		}
	}
}

func TestReview_RejectsInvalidActionsCorrectionsAndForeignResources(t *testing.T) {
	s := newReceiptServer(t)
	result := cleanReceipt()
	result.Total.Confidence = .7
	id := s.seed(s.alice, result)
	flag := receiptFlag(t, s, id, "low_confidence")
	path := "/review-flags/" + flag.ID.String() + "/resolve"
	if r := s.call(s.bob, "POST", path, `{"action":"confirm"}`); r.Code != 404 {
		t.Fatalf("foreign flag=%d", r.Code)
	}
	if r := s.call(s.alice, "POST", "/review-flags/"+uuid.NewString()+"/resolve", `{"action":"confirm"}`); r.Code != 404 {
		t.Fatalf("unknown flag=%d", r.Code)
	}
	for _, body := range []string{`{"action":"accept"}`, `{"action":"unknown"}`, `{"action":"correct"}`, `{"action":"correct","total":"10.00","storeName":"Extra"}`, `{"action":"correct","storeName":"Wrong field"}`, `{"action":"correct","total":"abc"}`, `{"action":"correct","total":""}`, `{"action":"correct","total":"1.234,56"}`, `{"action":"correct","lineItem":{"name":"BREAD","lineTotal":"10.00"}}`} {
		t.Run(body, func(t *testing.T) {
			r := s.call(s.alice, "POST", path, body)
			if r.Code != 400 {
				t.Fatalf("invalid=%d %s", r.Code, r.Body.String())
			}
		})
	}
	decodeReceipt(t, s.call(s.alice, "POST", path, `{"action":"confirm"}`), 200)
	if r := s.call(s.alice, "POST", path, `{"action":"confirm"}`); r.Code != 409 {
		t.Fatalf("repeated resolve=%d %s", r.Code, r.Body.String())
	}
	// A foreign product choice must roll back closing the flag.
	s = newReceiptServer(t)
	_, matchFlag, _ := prepareMatchFlag(t, s, "uncertain_match")
	foreign, err := s.q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: s.bob, CanonicalName: "Foreign product", Now: s.now})
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{"action":"accept"}`, `{"action":"confirm"}`, `{"action":"same_product"}`, `{"action":"different_product","canonicalName":""}`, `{"action":"different_product","canonicalName":"` + strings.Repeat("a", 201) + `"}`} {
		response := s.call(s.alice, "POST", "/review-flags/"+matchFlag.ID.String()+"/resolve", invalid)
		if response.Code != 400 {
			t.Fatalf("invalid product choice=%d %s", response.Code, response.Body.String())
		}
	}
	body := fmt.Sprintf(`{"action":"same_product","productId":%q}`, foreign.ID.String())
	r := s.call(s.alice, "POST", "/review-flags/"+matchFlag.ID.String()+"/resolve", body)
	if r.Code != 404 {
		t.Fatalf("foreign product=%d %s", r.Code, r.Body.String())
	}
	stored, err := s.q.GetOwnedReviewFlag(t.Context(), sqlc.GetOwnedReviewFlagParams{UserID: s.alice, FlagID: matchFlag.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ResolvedAt != nil {
		t.Fatal("failed product choice closed the flag")
	}
}

func TestReview_ListsOnlyOpenOwnedFlagsOldestReceiptFirst(t *testing.T) {
	s := newReceiptServer(t)
	result := cleanReceipt()
	result.Date.Found = false
	result.Total.Found = false
	first := s.seed(s.alice, result)
	secondResult := cleanReceipt()
	secondResult.Lines[0].Confidence = .7
	second := s.seed(s.alice, secondResult)
	s.seed(s.bob, result)
	if _, err := s.pool.Exec(t.Context(), "UPDATE receipts SET created_at=$1 WHERE user_id=$2 AND id=$3", s.now.Add(-time.Hour), s.alice, first); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	var receiptIDs []uuid.UUID
	var flagIDs []uuid.UUID
	for range 3 {
		path := "/review-flags?limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		r := s.call(s.alice, "GET", path, "")
		if r.Code != 200 {
			t.Fatalf("review list=%d %s", r.Code, r.Body.String())
		}
		var page httpapi.ReviewFlagPage
		if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if page.TotalOpen != 3 || len(page.Items) != 1 {
			t.Fatalf("page=%+v", page)
		}
		var document interface{}
		if err := json.Unmarshal(r.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if err := loadContract(t).Components.Schemas["ReviewFlagPage"].Value.VisitJSON(document); err != nil {
			t.Fatal(err)
		}
		entry := page.Items[0]
		receiptIDs = append(receiptIDs, entry.Receipt.Id)
		var base httpapi.ReviewFlagBase
		raw, err := json.Marshal(entry.Flag)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			t.Fatal(err)
		}
		flagIDs = append(flagIDs, base.Id)
		if entry.Receipt.Id == second && entry.LineItem == nil {
			t.Fatal("line flag has no line")
		}
		if page.NextCursor == nil {
			cursor = ""
		} else {
			cursor = *page.NextCursor
		}
	}
	if len(receiptIDs) != 3 || receiptIDs[0] != first || receiptIDs[1] != first || receiptIDs[2] != second || cursor != "" {
		t.Fatalf("review order=%v cursor=%s", receiptIDs, cursor)
	}
	decodeReceipt(t, s.call(s.alice, "POST", "/review-flags/"+flagIDs[0].String()+"/resolve", `{"action":"accept"}`), 200)
	r := s.call(s.alice, "GET", "/review-flags", "")
	var page httpapi.ReviewFlagPage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.TotalOpen != 2 || len(page.Items) != 2 {
		t.Fatalf("resolved flag still listed: %+v", page)
	}
	for _, path := range []string{"/review-flags?limit=0", "/review-flags?cursor=bad"} {
		if r := s.call(s.alice, "GET", path, ""); r.Code != 400 {
			t.Fatalf("invalid review page=%d", r.Code)
		}
	}
}

func TestReview_ConcurrentResolutionHasOneWinner(t *testing.T) {
	s := newReceiptServer(t)
	result := cleanReceipt()
	result.Total.Confidence = .7
	id := s.seed(s.alice, result)
	flag := receiptFlag(t, s, id, "low_confidence")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := s.service.Resolve(t.Context(), s.alice, flag.ID, receipts.Resolution{Action: "confirm"})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, apperr.Conflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestReview_ResolutionAuditFailureRollsBackLearning(t *testing.T) {
	s := newReceiptServer(t)
	_, flag, _ := prepareMatchFlag(t, s, "uncertain_match")
	if _, err := s.pool.Exec(t.Context(), "ALTER TABLE audit_events ADD CONSTRAINT synthetic_resolution_audit_failure CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	r := s.call(s.alice, "POST", "/review-flags/"+flag.ID.String()+"/resolve", `{"action":"different_product"}`)
	if r.Code != 500 {
		t.Fatalf("audit failure=%d %s", r.Code, r.Body.String())
	}
	stored, err := s.q.GetOwnedReviewFlag(t.Context(), sqlc.GetOwnedReviewFlagParams{UserID: s.alice, FlagID: flag.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ResolvedAt != nil {
		t.Fatal("failed audit closed the flag")
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM match_rejections WHERE user_id=$1", s.alice).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed resolution left rejections")
	}
	if strings.Contains(r.Body.String(), "synthetic_resolution_audit_failure") {
		t.Fatal("problem response leaked database details")
	}
}

func TestReview_DifferentProductDefaultsToLineName(t *testing.T) {
	s := newReceiptServer(t)
	_, flag, _ := prepareMatchFlag(t, s, "uncertain_match")
	response := decodeReceipt(t, s.call(s.alice, "POST", "/review-flags/"+flag.ID.String()+"/resolve", `{"action":"different_product"}`), 200)
	if response.LineItems[0].Product == nil || response.LineItems[0].Product.CanonicalName != "BREAD" {
		t.Fatalf("default canonical name=%+v", response.LineItems[0])
	}
}

func TestReview_LineCorrectionRerunsMatching(t *testing.T) {
	s := newReceiptServer(t)
	result := cleanReceipt()
	result.Lines[0].Confidence = .7
	id := s.seed(s.alice, result)
	before, err := s.service.Get(t.Context(), s.alice, id)
	if err != nil {
		t.Fatal(err)
	}
	flag := receiptFlag(t, s, id, "low_confidence")
	response := decodeReceipt(t, s.call(s.alice, "POST", "/review-flags/"+flag.ID.String()+"/resolve", `{"action":"correct","lineItem":{"name":"RICE 1 kg","quantity":"1","unitPrice":"10.00","lineTotal":"10.00"}}`), 200)
	if response.Status != "ready" || response.LineItems[0].Product == nil || response.LineItems[0].Product.Id == before.Lines[0].Item.ProductID.UUID || response.LineItems[0].RawName != "BREAD" {
		t.Fatalf("corrected line=%+v", response.LineItems[0])
	}
}

func TestResultHandler_ClassifiesOnlyValidGlobalCodesAsGTIN(t *testing.T) {
	for _, tc := range []struct {
		name, code, kind string
		store            bool
	}{
		{"valid global", "8690000000012", "gtin", true}, {"invalid checksum", "8690000000013", "store", true}, {"restricted circulation", "2001234000505", "store", true}, {"short weighed code", "2345678", "store", true}, {"global without store", "8690000000012", "gtin", false}, {"short without store", "2345678", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newReceiptServer(t)
			result := cleanReceipt()
			result.Lines[0].ProductCode = receiptField(tc.code)
			if !tc.store {
				result.StoreName.Found = false
			}
			s.seed(s.alice, result)
			rows, err := s.pool.Query(t.Context(), "SELECT kind,store_id FROM product_codes WHERE user_id=$1 AND code=$2", s.alice, tc.code)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				var kind string
				var storeID uuid.NullUUID
				if err := rows.Scan(&kind, &storeID); err != nil {
					t.Fatal(err)
				}
				count++
				if kind != tc.kind || storeID.Valid != (kind == "store") {
					t.Fatalf("kind=%s store=%v", kind, storeID.Valid)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			expected := 1
			if tc.kind == "" {
				expected = 0
			}
			if count != expected {
				t.Fatalf("codes=%d want %d", count, expected)
			}
		})
	}
}
