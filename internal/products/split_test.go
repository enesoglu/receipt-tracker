package products_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

// splitFixture is "Süt", which really is two products: one-litre and
// two-litre milk. "SUT" was seen for both, at different stores.
type splitFixture struct {
	*fixture
	milk, dairy uuid.UUID
	keep, move  []uuid.UUID
}

func newSplitFixture(t *testing.T) *splitFixture {
	f := &splitFixture{fixture: newFixture(t)}
	market := f.store(f.alice, "Market")
	corner := f.store(f.alice, "Corner")
	f.dairy = f.category(f.alice, "Dairy")
	f.milk = f.product(f.alice, "Süt")
	f.exec(`UPDATE products SET category_id = $1 WHERE id = $2`, f.dairy, f.milk)
	f.keep = []uuid.UUID{
		f.alias(f.alice, f.milk, "SUT 1L", market, 3),
		f.alias(f.alice, f.milk, "SUT", market, 2),
	}
	f.move = []uuid.UUID{
		f.alias(f.alice, f.milk, "SUT 2L", market, 2),
		f.alias(f.alice, f.milk, "SUT 2 LT", uuid.Nil, 1),
		f.alias(f.alice, f.milk, "SUT", corner, 1), // same name as a kept alias, other store
	}
	f.code(f.alice, f.milk, "8690000000017", "gtin", uuid.Nil, 1) // only on a moved line: moves
	f.code(f.alice, f.milk, "8690000000024", "gtin", uuid.Nil, 2) // on moved and kept lines: stays
	f.code(f.alice, f.milk, "77", "store", market, 1)             // only on a moved line at its store: moves
	f.code(f.alice, f.milk, "77", "store", corner, 1)             // its store's line stays: stays
	f.code(f.alice, f.milk, "99", "store", market, 1)             // on no line: stays

	atMarket := f.receipt(f.alice, receipt{store: market, date: "2026-10-01"})
	atCorner := f.receipt(f.alice, receipt{store: corner, date: "2026-10-02"})
	unknown := f.receipt(f.alice, receipt{date: "2026-10-03"})
	f.line(f.alice, atMarket, 0, line{name: "SUT 1L", total: "30.00", code: "8690000000024", product: f.milk})
	f.line(f.alice, atMarket, 1, line{name: "SUT 2L", total: "55.00", code: "77", product: f.milk})
	f.line(f.alice, atMarket, 2, line{name: "SUT", total: "31.00", product: f.milk})
	f.line(f.alice, atCorner, 0, line{name: "SUT 2L", total: "56.00", code: "8690000000017", product: f.milk})
	f.line(f.alice, atCorner, 1, line{name: "SUT", total: "57.00", code: "8690000000024", product: f.milk})
	f.line(f.alice, atCorner, 2, line{name: "SUT 1 L", total: "29.00", code: "77", product: f.milk})
	f.line(f.alice, unknown, 0, line{name: "SUT", total: "30.50", product: f.milk})
	return f
}

func TestSplit_MovesChosenNamesTheirLinesAndOwnCodes(t *testing.T) {
	f := newSplitFixture(t)
	bobBefore := f.snapshot(f.bob)

	d, err := f.service.Split(t.Context(), f.alice, f.milk, append(f.move, f.move[0]), "  Süt 2 L ")
	if err != nil {
		t.Fatal(err)
	}
	want := catalogue{
		Products: []string{"Süt", "Süt 2 L"},
		Aliases: []string{
			"Süt 2 L | SUT 2 LT | sut 2000 ml | - | 1",
			"Süt 2 L | SUT 2L | sut 2000 ml | Market | 2",
			"Süt 2 L | SUT | sut | Corner | 1",
			"Süt | SUT 1L | sut 1000 ml | Market | 3",
			"Süt | SUT | sut | Market | 2",
		},
		Codes: []string{
			"Süt 2 L | 77 | store | Market | 1",
			"Süt 2 L | 8690000000017 | gtin | - | 1",
			"Süt | 77 | store | Corner | 1",
			"Süt | 8690000000024 | gtin | - | 2",
			"Süt | 99 | store | Market | 1",
		},
		Lines: []string{
			"SUT 1 L @2 -> Süt (confirmed)",
			"SUT 1L @0 -> Süt (confirmed)",
			"SUT 2L @0 -> Süt 2 L (confirmed)",
			"SUT 2L @1 -> Süt 2 L (confirmed)",
			"SUT @0 -> Süt (confirmed)",
			"SUT @1 -> Süt 2 L (confirmed)",
			"SUT @2 -> Süt (confirmed)",
		},
		Rejections: []string{
			"Süt 2 L x sut 1000 ml | -",
			"Süt x sut 2000 ml | -",
		},
	}
	if diff := cmp.Diff(want, f.snapshot(f.alice)); diff != "" {
		t.Errorf("after split (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(bobBefore, f.snapshot(f.bob)); diff != "" {
		t.Errorf("bob's catalogue changed (-want +got):\n%s", diff)
	}
	if d.Product.CanonicalName != "Süt 2 L" || d.Category == nil || d.Category.ID != f.dairy ||
		!d.Product.SizeValue.Decimal.Equal(decimal.NewFromInt(2)) || *d.Product.SizeUnit != "l" || len(d.Aliases) != 3 {
		t.Errorf("new product = %+v", d)
	}

	var actions []string
	rows, err := f.pool.Query(t.Context(), `SELECT action || ':' || (detail->'lineItemIds'->>0 IS NOT NULL)::text FROM audit_events WHERE user_id = $1 ORDER BY action`, f.alice)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, a)
	}
	if diff := cmp.Diff([]string{"split:true", "split_from:true"}, actions); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestSplit_RefusesBadChoices(t *testing.T) {
	f := newSplitFixture(t)
	other := f.product(f.alice, "Kefir")
	otherAlias := f.alias(f.alice, other, "KEFIR", uuid.Nil, 1)
	bobProduct := f.product(f.bob, "Süt")
	bobAlias := f.alias(f.bob, bobProduct, "SUT", uuid.Nil, 1)
	before := f.snapshot(f.alice)
	all := append(append([]uuid.UUID{}, f.keep...), f.move...)
	for name, tc := range map[string]struct {
		user    uuid.UUID
		product uuid.UUID
		aliases []uuid.UUID
		newName string
		kind    apperr.Kind
	}{
		"no names":                {f.alice, f.milk, nil, "New", apperr.Invalid},
		"every name":              {f.alice, f.milk, all, "New", apperr.Invalid},
		"name of another product": {f.alice, f.milk, []uuid.UUID{f.move[0], otherAlias}, "New", apperr.Invalid},
		"name of another user":    {f.alice, f.milk, []uuid.UUID{bobAlias}, "New", apperr.Invalid},
		"blank new name":          {f.alice, f.milk, f.move, " ", apperr.Invalid},
		"product of another user": {f.bob, f.milk, f.move, "New", apperr.NotFound},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.service.Split(t.Context(), tc.user, tc.product, tc.aliases, tc.newName)
			if !errors.Is(err, tc.kind) {
				t.Errorf("err = %v, want %v", err, tc.kind)
			}
		})
	}
	if diff := cmp.Diff(before, f.snapshot(f.alice)); diff != "" {
		t.Errorf("catalogue changed (-want +got):\n%s", diff)
	}
}

// storedParser reads back the extract.Result a test stored as JSON.
type storedParser struct{}

func (storedParser) Parse(raw json.RawMessage) (extract.Result, error) {
	var result extract.Result
	err := json.Unmarshal(raw, &result)
	return result, err
}

func TestSplit_ReprocessingKeepsLinesOnTheSplitProduct(t *testing.T) {
	f := newFixture(t)
	market := f.store(f.alice, "Synthetic Market")
	ayran := f.product(f.alice, "Ayran")
	f.alias(f.alice, ayran, "AYRAN", market, 1)
	yayik := f.alias(f.alice, ayran, "YAYIK AYRAN", market, 1)

	field := func(value string) extract.Field[string] {
		return extract.Field[string]{Value: value, Confidence: .99, Found: true}
	}
	amount := func(value string) extract.Field[decimal.Decimal] {
		return extract.Field[decimal.Decimal]{Value: dec(value), Confidence: .99, Found: true}
	}
	result := extract.Result{
		StoreName: field("Synthetic Market"),
		Date:      extract.Field[time.Time]{Value: date("2026-11-01"), Confidence: .99, Found: true},
		Total:     amount("50.00"),
		Lines: []extract.Line{
			{Name: field("AYRAN"), Quantity: amount("1"), UnitPrice: amount("20.00"), Total: amount("20.00"), Confidence: .99},
			{Name: field("YAYIK AYRAN"), Quantity: amount("1"), UnitPrice: amount("30.00"), Total: amount("30.00"), Confidence: .99},
		},
	}
	q := sqlc.New(f.pool)
	receiptID := uuid.New()
	if err := q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: receiptID, UserID: f.alice, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: testNow()}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateExtraction(t.Context(), sqlc.CreateExtractionParams{UserID: f.alice, ReceiptID: receiptID, Provider: extract.ProviderFake, Model: "synthetic", Raw: raw, Now: testNow()}); err != nil {
		t.Fatal(err)
	}
	receiptService := receipts.New(f.pool, testNow, receipts.Options{Parsers: map[string]receipts.ResultParser{extract.ProviderFake: storedParser{}}})
	if err := receiptService.ApplyResult(t.Context(), f.alice, receiptID, result); err != nil {
		t.Fatal(err)
	}
	linked := func() []string {
		t.Helper()
		d, err := receiptService.Get(t.Context(), f.alice, receiptID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range d.Lines {
			name := "-"
			if l.Product != nil {
				name = l.Product.CanonicalName
			}
			out = append(out, l.Item.Name+" -> "+name+" ("+l.Item.MatchStatus+")")
		}
		return out
	}
	if diff := cmp.Diff([]string{"AYRAN -> Ayran (auto_linked)", "YAYIK AYRAN -> Ayran (auto_linked)"}, linked()); diff != "" {
		t.Fatalf("before split (-want +got):\n%s", diff)
	}

	if _, err := f.service.Split(t.Context(), f.alice, ayran, []uuid.UUID{yayik}, "Yayık Ayran"); err != nil {
		t.Fatal(err)
	}
	want := []string{"AYRAN -> Ayran (auto_linked)", "YAYIK AYRAN -> Yayık Ayran (auto_linked)"}
	if diff := cmp.Diff(want, linked()); diff != "" {
		t.Errorf("after split (-want +got):\n%s", diff)
	}
	if _, err := receiptService.Reprocess(t.Context(), f.alice, receiptID); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, linked()); diff != "" {
		t.Errorf("after reprocessing (-want +got):\n%s", diff)
	}
}

func TestSplit_CopiesThePriceBasis(t *testing.T) {
	f := newFixture(t)
	chicken := f.product(f.alice, "Tavuk")
	f.exec(`UPDATE products SET price_basis = 'kg' WHERE id = $1`, chicken)
	f.alias(f.alice, chicken, "TAVUK BUT 1KG", uuid.Nil, 1)
	wings := f.alias(f.alice, chicken, "TAVUK KANAT 500G", uuid.Nil, 1)

	d, err := f.service.Split(t.Context(), f.alice, chicken, []uuid.UUID{wings}, "Tavuk Kanat 500 G")
	if err != nil {
		t.Fatal(err)
	}
	if d.Product.PriceBasis != "kg" || d.Product.SizeValue.Valid {
		t.Errorf("new product = %+v", d.Product)
	}
}
