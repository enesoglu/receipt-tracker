package httpapi_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/httpapi"
)

func TestReceipts_RejectsNullBytesWithoutChangingData(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	base := "/receipts/" + id.String()
	before := s.call(s.alice, http.MethodGet, base, "").Body.String()
	for _, test := range []struct{ name, method, path, body string }{
		{"title", "PUT", base, `{"title":"bad\u0000title","currency":"TRY"}`},
		{"store", "PUT", base, `{"title":"Valid","storeName":"bad\u0000store","currency":"TRY"}`},
		{"line name", "POST", base + "/line-items", `{"name":"bad\u0000name","lineTotal":"1.00"}`},
		{"unit", "POST", base + "/line-items", `{"name":"Valid","quantityUnit":"a\u0000d","lineTotal":"1.00"}`},
		{"code", "POST", base + "/line-items", `{"name":"Valid","productCode":"bad\u0000code","lineTotal":"1.00"}`},
		{"category", "POST", "/categories", `{"name":"bad\u0000category"}`},
		{"settings", "PUT", "/settings", `{"defaultCurrency":"TRY","defaultReceiptTitle":"bad\u0000title"}`},
		{"search", "GET", "/receipts?q=bad%00query", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			decodeAs[httpapi.Problem](t, s.call(s.alice, test.method, test.path, test.body), http.StatusBadRequest, "Problem")
		})
	}
	if after := s.call(s.alice, http.MethodGet, base, "").Body.String(); after != before {
		t.Fatal("invalid text changed the receipt")
	}
}

func TestProducts_RejectsNullBytesInNamesUnitsAndCursors(t *testing.T) {
	s := newProductServer(t)
	first := s.seedMilk(s.alice)
	s.exec(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name) VALUES ($1, $2, 'MILK', 'milk')`, s.alice, first)
	detail := decodeAs[httpapi.Product](t, s.get(s.alice, "/products/"+first.String()), http.StatusOK, "Product")
	s.id(`INSERT INTO products (user_id, canonical_name) VALUES ($1, 'Z synthetic') RETURNING id`, s.alice)
	page := decodeAs[httpapi.ProductPage](t, s.get(s.alice, "/products?limit=1"), http.StatusOK, "ProductPage")
	if page.NextCursor == nil {
		t.Fatal("missing cursor")
	}
	base := "/products/" + first.String()
	for _, test := range []struct{ name, method, path, body string }{
		{"name", "PUT", base, `{"canonicalName":"bad\u0000name"}`},
		{"unit", "PUT", base, `{"canonicalName":"Valid","sizeValue":"1.000","sizeUnit":"k\u0000g"}`},
		{"split name", "POST", base + "/split", `{"canonicalName":"bad\u0000name","aliasIds":["` + detail.Aliases[0].Id.String() + `"]}`},
		{"search", "GET", "/products?q=bad%00query", ""},
		{"cursor", "GET", "/products?limit=1&cursor=" + url.QueryEscape(cursorWithNullName(t, *page.NextCursor)), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			decodeAs[httpapi.Problem](t, s.call(s.alice, test.method, test.path, test.body), http.StatusBadRequest, "Problem")
		})
	}
	after := decodeAs[httpapi.Product](t, s.get(s.alice, base), http.StatusOK, "Product")
	if after.CanonicalName != "Süt 1 L" {
		t.Fatal("invalid text changed the product")
	}
}

func TestCatalog_RejectsNullBytesInCursor(t *testing.T) {
	s := newReceiptServer(t)
	for _, name := range []string{"Synthetic A", "Synthetic B"} {
		decodeAs[httpapi.Category](t, s.call(s.alice, "POST", "/categories", `{"name":"`+name+`"}`), http.StatusCreated, "Category")
	}
	page := decodeAs[httpapi.CategoryPage](t, s.call(s.alice, "GET", "/categories?limit=1", ""), http.StatusOK, "CategoryPage")
	if page.NextCursor == nil {
		t.Fatal("missing cursor")
	}
	path := "/categories?limit=1&cursor=" + url.QueryEscape(cursorWithNullName(t, *page.NextCursor))
	decodeAs[httpapi.Problem](t, s.call(s.alice, "GET", path, ""), http.StatusBadRequest, "Problem")
}

func cursorWithNullName(t *testing.T, value string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	var cursor map[string]any
	if err := json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	cursor["n"] = "bad\x00name"
	raw, err = json.Marshal(cursor)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
