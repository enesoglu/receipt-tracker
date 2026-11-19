package httpapi_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
)

func TestStores_ListRenameConflictAndIsolation(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	s.seed(s.alice, cleanReceipt())
	other := cleanReceipt()
	other.StoreName = receiptField("Alpha Market")
	otherID := s.seed(s.alice, other)
	d, err := s.service.Get(t.Context(), s.alice, id)
	if err != nil {
		t.Fatal(err)
	}
	otherDetail, err := s.service.Get(t.Context(), s.alice, otherID)
	if err != nil {
		t.Fatal(err)
	}
	r := s.call(s.alice, "GET", "/stores?limit=1", "")
	var page httpapi.StorePage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || len(page.Items) != 1 || page.Items[0].Name != "Alpha Market" || page.NextCursor == nil {
		t.Fatalf("stores=%d %s", r.Code, r.Body.String())
	}
	r = s.call(s.alice, "GET", "/stores?limit=1&cursor="+url.QueryEscape(*page.NextCursor), "")
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || len(page.Items) != 1 || page.Items[0].Id != d.Store.ID || page.Items[0].ReceiptCount != 2 || page.NextCursor != nil {
		t.Fatalf("stores=%d %s", r.Code, r.Body.String())
	}
	path := "/stores/" + d.Store.ID.String()
	if r := s.call(s.bob, "PUT", path, `{"name":"Forbidden"}`); r.Code != 404 {
		t.Fatalf("foreign rename=%d", r.Code)
	}
	r = s.call(s.alice, "PUT", path, `{"name":"Beta Market"}`)
	var store httpapi.Store
	if err := json.Unmarshal(r.Body.Bytes(), &store); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || store.Name != "Beta Market" || store.ReceiptCount != 2 {
		t.Fatalf("rename=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.alice, "PUT", "/stores/"+otherDetail.Store.ID.String(), `{"name":"BETA MARKET!"}`); r.Code != 409 {
		t.Fatalf("duplicate store=%d %s", r.Code, r.Body.String())
	}
	r = s.call(s.bob, "GET", "/stores", "")
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || len(page.Items) != 0 {
		t.Fatalf("store isolation=%d %s", r.Code, r.Body.String())
	}
	var before, after string
	if err := s.pool.QueryRow(t.Context(), "SELECT detail->'before'->>'Name',detail->'after'->>'Name' FROM audit_events WHERE user_id=$1 AND entity='store'", s.alice).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	if before != "Synthetic Market" || after != "Beta Market" {
		t.Fatalf("store audit=%s -> %s", before, after)
	}
}

func TestCategories_CRUDPagingCountsAndIsolation(t *testing.T) {
	s := newReceiptServer(t)
	if _, err := s.pool.Exec(t.Context(), "DELETE FROM categories WHERE user_id=$1", s.alice); err != nil {
		t.Fatal(err)
	}
	create := func(user uuid.UUID, name string) httpapi.Category {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"name": name})
		r := s.call(user, "POST", "/categories", string(body))
		if r.Code != 201 {
			t.Fatalf("create=%d %s", r.Code, r.Body.String())
		}
		var category httpapi.Category
		if err := json.Unmarshal(r.Body.Bytes(), &category); err != nil {
			t.Fatal(err)
		}
		return category
	}
	a := create(s.alice, "Alpha")
	b := create(s.alice, "beta")
	foreign := create(s.bob, "Private category")
	if r := s.call(s.alice, "POST", "/categories", `{"name":"ALPHA"}`); r.Code != 409 {
		t.Fatalf("duplicate=%d", r.Code)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		body := ""
		if method == "PUT" {
			body = `{"name":"Cannot edit"}`
		}
		r := s.call(s.bob, method, "/categories/"+a.Id.String(), body)
		if r.Code != 404 {
			t.Fatalf("foreign %s=%d %s", method, r.Code, r.Body.String())
		}
	}
	r := s.call(s.alice, "GET", "/categories?limit=1", "")
	var page httpapi.CategoryPage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || len(page.Items) != 1 || page.Items[0].Id != a.Id || page.NextCursor == nil {
		t.Fatalf("category page=%d %s", r.Code, r.Body.String())
	}
	cursor := *page.NextCursor
	r = s.call(s.alice, "GET", "/categories?limit=1&cursor="+url.QueryEscape(cursor), "")
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || len(page.Items) != 1 || page.Items[0].Id != b.Id || page.NextCursor != nil {
		t.Fatalf("category page=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.bob, "GET", "/categories?cursor="+url.QueryEscape(cursor), ""); r.Code != 400 {
		t.Fatalf("foreign cursor=%d", r.Code)
	}
	receiptID := s.seed(s.alice, cleanReceipt())
	d, err := s.service.Get(t.Context(), s.alice, receiptID)
	if err != nil {
		t.Fatal(err)
	}
	productID := d.Lines[0].Item.ProductID.UUID
	if _, err := s.pool.Exec(t.Context(), "UPDATE products SET category_id=$1 WHERE user_id=$2 AND id=$3", a.Id, s.alice, productID); err != nil {
		t.Fatal(err)
	}
	r = s.call(s.alice, "PUT", "/categories/"+a.Id.String(), `{"name":"Gamma"}`)
	var renamed httpapi.Category
	if err := json.Unmarshal(r.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || renamed.ProductCount != 1 || renamed.Name != "Gamma" {
		t.Fatalf("rename=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.alice, "PUT", "/categories/"+b.Id.String(), `{"name":"gamma"}`); r.Code != 409 {
		t.Fatalf("duplicate rename=%d", r.Code)
	}
	if r := s.call(s.alice, "DELETE", "/categories/"+a.Id.String(), ""); r.Code != 204 {
		t.Fatalf("delete=%d %s", r.Code, r.Body.String())
	}
	product, err := s.q.GetReviewProduct(t.Context(), sqlc.GetReviewProductParams{UserID: s.alice, ProductID: productID})
	if err != nil {
		t.Fatal(err)
	}
	if product.CategoryID.Valid {
		t.Fatal("category deletion did not uncategorise the product")
	}
	r = s.call(s.alice, "GET", "/categories", "")
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Id != b.Id || page.Items[0].Id == foreign.Id {
		t.Fatalf("category isolation=%+v", page)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE user_id=$1 AND entity='category' AND detail ? 'before' AND detail ? 'after'", s.alice).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("category audits=%d", count)
	}
}

func TestSettings_DefaultsAreIsolatedAndDoNotAlterExistingReceipts(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	r := s.call(s.alice, "GET", "/settings", "")
	var original httpapi.Settings
	if err := json.Unmarshal(r.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 {
		t.Fatalf("get settings=%d", r.Code)
	}
	r = s.call(s.bob, "PUT", "/settings", `{"defaultCurrency":"EUR","defaultReceiptTitle":"Bob defaults","llmSuggestionsEnabled":false}`)
	if r.Code != 200 {
		t.Fatalf("bob update=%d %s", r.Code, r.Body.String())
	}
	r = s.call(s.alice, "GET", "/settings", "")
	var unchanged httpapi.Settings
	if err := json.Unmarshal(r.Body.Bytes(), &unchanged); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(original, unchanged); diff != "" {
		t.Fatal(diff)
	}
	r = s.call(s.alice, "PUT", "/settings", `{"defaultCurrency":"USD","defaultReceiptTitle":"New default","llmSuggestionsEnabled":false}`)
	var updated httpapi.Settings
	if err := json.Unmarshal(r.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || updated.DefaultCurrency != "USD" || updated.DefaultReceiptTitle != "New default" {
		t.Fatalf("settings=%d %s", r.Code, r.Body.String())
	}
	if updated.LlmSuggestionsEnabled {
		t.Fatal("the save did not switch suggestions off")
	}
	if original.LlmSuggestionsEnabled != true {
		t.Fatal("a new user does not have suggestions switched on")
	}
	defaults, err := s.q.GetUploadDefaults(t.Context(), s.alice)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.DefaultCurrency != "USD" || defaults.DefaultReceiptTitle != "New default" {
		t.Fatalf("future upload defaults=%+v", defaults)
	}
	d, err := s.service.Get(t.Context(), s.alice, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Receipt.Currency != "TRY" || d.Receipt.Title != "Synthetic receipt" {
		t.Fatal("settings altered an existing receipt")
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE user_id=$1 AND entity='settings' AND detail ? 'before' AND detail ? 'after'", s.alice).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("settings audits=%d", count)
	}
}

func TestCatalog_RejectsInvalidInputAndMissingResources(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	d, err := s.service.Get(t.Context(), s.alice, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/stores?limit=0", "", 400}, {"GET", "/stores?cursor=bad", "", 400}, {"GET", "/categories?limit=101", "", 400}, {"GET", "/categories?cursor=bad", "", 400}, {"POST", "/categories", `{"name":""}`, 400}, {"POST", "/categories", `{"name":"` + strings.Repeat("a", 101) + `"}`, 400}, {"PUT", "/stores/" + d.Store.ID.String(), `{"name":"!!!"}`, 400}, {"PUT", "/stores/" + uuid.NewString(), `{"name":"Missing"}`, 404}, {"PUT", "/categories/" + uuid.NewString(), `{"name":"Missing"}`, 404}, {"DELETE", "/categories/" + uuid.NewString(), "", 404}, {"PUT", "/settings", `{"defaultCurrency":"usd","defaultReceiptTitle":"A","llmSuggestionsEnabled":true}`, 400}, {"PUT", "/settings", `{"defaultCurrency":"TRY","defaultReceiptTitle":"","llmSuggestionsEnabled":true}`, 400},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			r := s.call(s.alice, tc.method, tc.path, tc.body)
			if r.Code != tc.status {
				t.Fatalf("status=%d want %d: %s", r.Code, tc.status, r.Body.String())
			}
		})
	}
}
