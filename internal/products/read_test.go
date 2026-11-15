package products_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/products"
)

func names(page products.Page) []string {
	out := []string{}
	for _, item := range page.Items {
		out = append(out, item.Product.CanonicalName)
	}
	return out
}

func TestList_SearchesAliasesAndCanonicalNamesLikeReceiptLines(t *testing.T) {
	f := newFixture(t)
	milk := f.product(f.alice, "Pınar Süt 1 L")
	f.alias(f.alice, milk, "PNR SUT 1LT", uuid.Nil, 1)
	ayran := f.product(f.alice, "Ayran")
	f.alias(f.alice, ayran, "AYRAN 1000ML", uuid.Nil, 1)
	f.product(f.alice, "Çiğ Köfte")
	f.product(f.alice, "Yoğurt")
	bobMilk := f.product(f.bob, "Süt")
	f.alias(f.bob, bobMilk, "SUT", uuid.Nil, 1)

	for _, tc := range []struct {
		search string
		want   []string
	}{
		{"", []string{"Ayran", "Pınar Süt 1 L", "Yoğurt", "Çiğ Köfte"}},
		{"süt", []string{"Pınar Süt 1 L"}},
		{"SUT", []string{"Pınar Süt 1 L"}},
		// The size is compared in its base unit: 1 l is 1000 ml.
		{"1 l", []string{"Ayran", "Pınar Süt 1 L"}},
		{"cig kofte", []string{"Çiğ Köfte"}},
		{"YOĞURT", []string{"Yoğurt"}},
		{"pnr", []string{"Pınar Süt 1 L"}},
		{"süt ayran", []string{}},
		{"%%", []string{}},
	} {
		t.Run(tc.search, func(t *testing.T) {
			page, err := f.service.List(t.Context(), f.alice, products.Filter{Search: tc.search})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, names(page), cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("names (-want +got):\n%s", diff)
			}
		})
	}
}

func TestList_FiltersByCategoryAndPagesInNameOrder(t *testing.T) {
	f := newFixture(t)
	dairy := f.category(f.alice, "Dairy")
	for _, name := range []string{"Beyaz Peynir", "Ayran", "Kefir", "Süt", "Tereyağı"} {
		id := f.product(f.alice, name)
		f.exec(`UPDATE products SET category_id = $1 WHERE id = $2`, dairy, id)
	}
	f.product(f.alice, "Ekmek")
	// Same name twice: the id breaks the tie, so no product is skipped.
	twin := f.product(f.alice, "Kefir")
	f.exec(`UPDATE products SET category_id = $1 WHERE id = $2`, dairy, twin)

	var got []string
	filter := products.Filter{CategoryID: uuid.NullUUID{UUID: dairy, Valid: true}, Limit: 2}
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging did not end")
		}
		page, err := f.service.List(t.Context(), f.alice, filter)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, names(page)...)
		if page.NextCursor == nil {
			break
		}
		filter.Cursor = *page.NextCursor
	}
	want := []string{"Ayran", "Beyaz Peynir", "Kefir", "Kefir", "Süt", "Tereyağı"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("names (-want +got):\n%s", diff)
	}

	// Bob's view of Alice's category is empty: the filter is scoped too.
	page, err := f.service.List(t.Context(), f.bob, products.Filter{CategoryID: uuid.NullUUID{UUID: dairy, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Errorf("bob sees %v", names(page))
	}
}

func TestList_RefusesBadLimitsAndForeignCursors(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		f.product(f.alice, "Product")
	}
	page, err := f.service.List(t.Context(), f.alice, products.Filter{Limit: 1})
	if err != nil || page.NextCursor == nil {
		t.Fatalf("first page: %v, %v", page, err)
	}
	for name, tc := range map[string]struct {
		user   uuid.UUID
		filter products.Filter
	}{
		"limit too large":       {f.alice, products.Filter{Limit: 101}},
		"negative limit":        {f.alice, products.Filter{Limit: -1}},
		"cursor of other user":  {f.bob, products.Filter{Cursor: *page.NextCursor}},
		"cursor of other query": {f.alice, products.Filter{Search: "x", Cursor: *page.NextCursor}},
		"garbage cursor":        {f.alice, products.Filter{Cursor: "not-a-cursor"}},
		"search too long":       {f.alice, products.Filter{Search: strings.Repeat("a", 101)}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.service.List(t.Context(), tc.user, tc.filter)
			if !errors.Is(err, apperr.Invalid) {
				t.Errorf("err = %v, want Invalid", err)
			}
		})
	}
}

func TestList_CountsOnlyPurchasesThatCountForPriceHistory(t *testing.T) {
	f := newFixture(t)
	market := f.store(f.alice, "Synthetic Market")
	milk := f.product(f.alice, "Süt")
	add := func(r receipt, l line) {
		l.product = milk
		f.line(f.alice, f.receipt(f.alice, r), 0, l)
	}
	add(receipt{store: market, date: "2026-10-01"}, line{unitPrice: "30.00", total: "30.00"})
	add(receipt{date: "2026-10-03", status: "needs_review"}, line{quantity: "2", total: "65.00"})
	add(receipt{store: market, date: "2026-10-02", time: "09:00"}, line{total: "31.00"})
	// None of these count: wrong status, no date, discount, no price.
	add(receipt{date: "2026-11-01", status: "failed"}, line{total: "99.00"})
	add(receipt{date: "2026-11-01", status: "processing"}, line{total: "99.00"})
	add(receipt{status: "ready"}, line{total: "99.00"})
	add(receipt{date: "2026-11-01"}, line{total: "-5.00"})
	add(receipt{date: "2026-11-01"}, line{})
	f.product(f.alice, "Unbought")

	page, err := f.service.List(t.Context(), f.alice, products.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	type summary struct {
		Name  string
		Count int
		Last  *products.Purchase
	}
	var got []summary
	for _, item := range page.Items {
		got = append(got, summary{item.Product.CanonicalName, item.PurchaseCount, item.LastPurchase})
	}
	want := []summary{
		{"Süt", 3, &products.Purchase{PurchasedOn: date("2026-10-03"), UnitPrice: dec("32.50"), Currency: "TRY"}},
		{"Unbought", 0, nil},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("summaries (-want +got):\n%s", diff)
	}
}

func TestGet_ReturnsAliasesMostSeenFirstAndCodesWithStores(t *testing.T) {
	f := newFixture(t)
	market := f.store(f.alice, "Synthetic Market")
	corner := f.store(f.alice, "Corner Shop")
	dairy := f.category(f.alice, "Dairy")
	milk := f.product(f.alice, "Süt 1 L")
	f.exec(`UPDATE products SET category_id = $1, size_value = 1, size_unit = 'l' WHERE id = $2`, dairy, milk)
	rare := f.alias(f.alice, milk, "SUT 1 LT", corner, 1)
	common := f.alias(f.alice, milk, "SUT 1L", market, 5)
	unknown := f.alias(f.alice, milk, "SÜT", uuid.Nil, 2)
	gtin := f.code(f.alice, milk, "4006381333931", "gtin", uuid.Nil, 3)
	storeCode := f.code(f.alice, milk, "004512", "store", market, 4)
	f.line(f.alice, f.receipt(f.alice, receipt{store: market, date: "2026-11-02"}), 0, line{unitPrice: "42.25", total: "42.25", product: milk})

	d, err := f.service.Get(t.Context(), f.alice, milk)
	if err != nil {
		t.Fatal(err)
	}
	type alias struct {
		ID        uuid.UUID
		Raw, Key  string
		Store     *products.Ref
		SeenCount int32
	}
	var aliases []alias
	for _, a := range d.Aliases {
		aliases = append(aliases, alias{a.ID, a.RawName, a.NormalizedName, a.Store, a.SeenCount})
	}
	wantAliases := []alias{
		{common, "SUT 1L", "sut 1000 ml", &products.Ref{ID: market, Name: "Synthetic Market"}, 5},
		{unknown, "SÜT", "sut", nil, 2},
		{rare, "SUT 1 LT", "sut 1000 ml", &products.Ref{ID: corner, Name: "Corner Shop"}, 1},
	}
	if diff := cmp.Diff(wantAliases, aliases); diff != "" {
		t.Errorf("aliases (-want +got):\n%s", diff)
	}
	type code struct {
		ID         uuid.UUID
		Code, Kind string
		Store      *products.Ref
	}
	var codes []code
	for _, c := range d.Codes {
		codes = append(codes, code{c.ID, c.Code, c.Kind, c.Store})
	}
	wantCodes := []code{
		{storeCode, "004512", "store", &products.Ref{ID: market, Name: "Synthetic Market"}},
		{gtin, "4006381333931", "gtin", nil},
	}
	if diff := cmp.Diff(wantCodes, codes); diff != "" {
		t.Errorf("codes (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(&products.Ref{ID: dairy, Name: "Dairy"}, d.Category); diff != "" {
		t.Errorf("category (-want +got):\n%s", diff)
	}
	if d.PurchaseCount != 1 || d.LastPurchase == nil || d.LastPurchase.Store == nil || d.LastPurchase.Store.ID != market {
		t.Errorf("purchases = %d, %+v", d.PurchaseCount, d.LastPurchase)
	}
}

func TestGet_OtherUsersProductIsNotFound(t *testing.T) {
	f := newFixture(t)
	milk := f.product(f.alice, "Süt")
	for name, id := range map[string]uuid.UUID{"other user": milk, "missing": uuid.New()} {
		t.Run(name, func(t *testing.T) {
			_, err := f.service.Get(t.Context(), f.bob, id)
			if !errors.Is(err, apperr.NotFound) {
				t.Errorf("err = %v, want NotFound", err)
			}
		})
	}
}
