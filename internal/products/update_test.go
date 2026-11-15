package products_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/products"
)

func TestUpdate_ReplacesEveryFieldAndAuditsTheChange(t *testing.T) {
	f := newFixture(t)
	dairy := f.category(f.alice, "Dairy")
	milk := f.product(f.alice, "SUT 1LT")
	f.exec(`UPDATE products SET size_value = 1, size_unit = 'l' WHERE id = $1`, milk)

	d, err := f.service.Update(t.Context(), f.alice, milk, products.Update{
		CanonicalName: "  Süt 2 L ", CategoryID: uuid.NullUUID{UUID: dairy, Valid: true},
		SizeValue: decimal.NewNullDecimal(dec("2")), SizeUnit: ptr(" l "),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Product.CanonicalName != "Süt 2 L" || d.Category == nil || d.Category.ID != dairy ||
		!d.Product.SizeValue.Decimal.Equal(dec("2")) || *d.Product.SizeUnit != "l" || !d.Product.UpdatedAt.Equal(testNow()) {
		t.Errorf("product = %+v, category = %+v", d.Product, d.Category)
	}

	// Leaving out category and size clears them.
	d, err = f.service.Update(t.Context(), f.alice, milk, products.Update{CanonicalName: "Süt"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Category != nil || d.Product.SizeValue.Valid || d.Product.SizeUnit != nil {
		t.Errorf("product = %+v, category = %+v", d.Product, d.Category)
	}

	var details []json.RawMessage
	rows, err := f.pool.Query(t.Context(), `SELECT detail FROM audit_events WHERE user_id = $1 AND entity = 'product' AND entity_id = $2 AND action = 'updated' ORDER BY id`, f.alice, milk)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		details = append(details, raw)
	}
	if len(details) != 2 {
		t.Fatalf("audit events = %d, want 2", len(details))
	}
	var first map[string]map[string]any
	if err := json.Unmarshal(details[0], &first); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]any{
		"before": {"canonicalName": "SUT 1LT", "categoryId": nil, "sizeValue": "1", "sizeUnit": "l"},
		"after":  {"canonicalName": "Süt 2 L", "categoryId": dairy.String(), "sizeValue": "2", "sizeUnit": "l"},
	}
	if diff := cmp.Diff(want, first); diff != "" {
		t.Errorf("audit detail (-want +got):\n%s", diff)
	}
}

func TestUpdate_RefusesInvalidInput(t *testing.T) {
	f := newFixture(t)
	milk := f.product(f.alice, "Süt")
	bobsCategory := f.category(f.bob, "Breakfast")
	for name, u := range map[string]products.Update{
		"blank name":           {CanonicalName: "  "},
		"long name":            {CanonicalName: strings.Repeat("a", 201)},
		"value without unit":   {CanonicalName: "Süt", SizeValue: decimal.NewNullDecimal(dec("1"))},
		"unit without value":   {CanonicalName: "Süt", SizeUnit: ptr("l")},
		"zero size":            {CanonicalName: "Süt", SizeValue: decimal.NewNullDecimal(dec("0")), SizeUnit: ptr("l")},
		"negative size":        {CanonicalName: "Süt", SizeValue: decimal.NewNullDecimal(dec("-1")), SizeUnit: ptr("l")},
		"four decimal places":  {CanonicalName: "Süt", SizeValue: decimal.NewNullDecimal(dec("1.0005")), SizeUnit: ptr("l")},
		"too large":            {CanonicalName: "Süt", SizeValue: decimal.NewNullDecimal(dec("1000000000")), SizeUnit: ptr("l")},
		"blank unit":           {CanonicalName: "Süt", SizeValue: decimal.NewNullDecimal(dec("1")), SizeUnit: ptr(" ")},
		"other users category": {CanonicalName: "Süt", CategoryID: uuid.NullUUID{UUID: bobsCategory, Valid: true}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.service.Update(t.Context(), f.alice, milk, u)
			if !errors.Is(err, apperr.Invalid) {
				t.Errorf("err = %v, want Invalid", err)
			}
		})
	}
	var name string
	if err := f.pool.QueryRow(t.Context(), `SELECT canonical_name FROM products WHERE id = $1`, milk).Scan(&name); err != nil || name != "Süt" {
		t.Errorf("name = %q, %v", name, err)
	}
}

func TestUpdate_OtherUsersProductIsNotFoundAndUnchanged(t *testing.T) {
	f := newFixture(t)
	milk := f.product(f.alice, "Süt")
	_, err := f.service.Update(t.Context(), f.bob, milk, products.Update{CanonicalName: "Taken"})
	if !errors.Is(err, apperr.NotFound) {
		t.Fatalf("err = %v, want NotFound", err)
	}
	var name string
	var audits int
	if err := f.pool.QueryRow(t.Context(), `SELECT canonical_name, (SELECT count(*) FROM audit_events) FROM products WHERE id = $1`, milk).Scan(&name, &audits); err != nil {
		t.Fatal(err)
	}
	if name != "Süt" || audits != 0 {
		t.Errorf("name = %q, audits = %d", name, audits)
	}
}
