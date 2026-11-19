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

func TestStores_CreateDetailMergeDeleteAndIsolation(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	d, err := s.service.Get(t.Context(), s.alice, id)
	if err != nil {
		t.Fatal(err)
	}
	market := d.Store.ID

	// A store made by hand starts empty: no receipts, no tax number, no
	// raw names.
	r := s.call(s.alice, "POST", "/stores", `{"name":"  Corner Shop  "}`)
	var created httpapi.Store
	if err := json.Unmarshal(r.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if r.Code != 201 || created.Name != "Corner Shop" || created.ReceiptCount != 0 || created.Vkn != nil || created.AliasCount != 0 {
		t.Fatalf("create=%d %s", r.Code, r.Body.String())
	}
	// The same comparison name is a conflict, and the same display name is
	// free for another user.
	if r := s.call(s.alice, "POST", "/stores", `{"name":"CORNER SHOP!"}`); r.Code != 409 {
		t.Fatalf("duplicate create=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.bob, "POST", "/stores", `{"name":"Corner Shop"}`); r.Code != 201 {
		t.Fatalf("second user create=%d %s", r.Code, r.Body.String())
	}

	// The detail shows what resolution built — every raw name seen — and
	// the branch names the store's receipts printed, both read-only.
	s.seed(s.alice, cleanReceipt())
	if r := s.call(s.alice, "PUT", "/receipts/"+id.String(), `{"title":"Branch receipt","currency":"TRY","storeName":"Synthetic Market","branchName":" ANKARA ÇANKAYA "}`); r.Code != 200 {
		t.Fatalf("branch edit=%d %s", r.Code, r.Body.String())
	}
	r = s.call(s.alice, "GET", "/stores/"+market.String(), "")
	var detail httpapi.StoreDetail
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || detail.Id != market || detail.Name != "Synthetic Market" || detail.ReceiptCount != 2 || detail.Vkn != nil || detail.AliasCount != 1 {
		t.Fatalf("detail=%d %s", r.Code, r.Body.String())
	}
	// The typed store name is remembered as a third sighting.
	if len(detail.Aliases) != 1 || detail.Aliases[0].RawName != "Synthetic Market" || detail.Aliases[0].SeenCount != 3 || !detail.Aliases[0].LastSeenAt.Equal(s.now) {
		t.Fatalf("detail aliases=%+v", detail.Aliases)
	}
	if len(detail.Branches) != 1 || detail.Branches[0].Name != "ANKARA ÇANKAYA" || detail.Branches[0].ReceiptCount != 1 {
		t.Fatalf("detail branches=%+v", detail.Branches)
	}

	// A receipt naming a second store gives the merge receipts and raw
	// names to move.
	beta := cleanReceipt()
	beta.StoreName = receiptField("Beta Market")
	betaID := s.seed(s.alice, beta)
	bd, err := s.service.Get(t.Context(), s.alice, betaID)
	if err != nil {
		t.Fatal(err)
	}
	r = s.call(s.alice, "POST", "/stores/"+market.String()+"/merge", `{"sourceId":"`+bd.Store.ID.String()+`"}`)
	var merged httpapi.StoreDetail
	if err := json.Unmarshal(r.Body.Bytes(), &merged); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || merged.Id != market || merged.ReceiptCount != 3 || merged.AliasCount != 2 {
		t.Fatalf("merge=%d %s", r.Code, r.Body.String())
	}
	if len(merged.Aliases) != 2 || merged.Aliases[0].RawName != "Beta Market" || merged.Aliases[0].SeenCount != 1 || merged.Aliases[1].RawName != "Synthetic Market" || merged.Aliases[1].SeenCount != 3 {
		t.Fatalf("merged aliases=%+v", merged.Aliases)
	}
	if len(merged.Branches) != 1 || merged.Branches[0].Name != "ANKARA ÇANKAYA" {
		t.Fatalf("merged branches=%+v", merged.Branches)
	}
	if r := s.call(s.alice, "GET", "/stores/"+bd.Store.ID.String(), ""); r.Code != 404 {
		t.Fatalf("source after merge=%d", r.Code)
	}
	if r := s.call(s.alice, "POST", "/stores/"+market.String()+"/merge", `{"sourceId":"`+market.String()+`"}`); r.Code != 400 {
		t.Fatalf("self merge=%d", r.Code)
	}

	// Two different tax numbers are a conflict naming both: two legal
	// entities should not silently become one.
	if _, err := s.pool.Exec(t.Context(), "UPDATE stores SET vkn='1234567890' WHERE user_id=$1 AND id=$2", s.alice, created.Id); err != nil {
		t.Fatal(err)
	}
	r = s.call(s.alice, "POST", "/stores", `{"name":"Gamma Market"}`)
	var gamma httpapi.Store
	if err := json.Unmarshal(r.Body.Bytes(), &gamma); err != nil {
		t.Fatal(err)
	}
	if r.Code != 201 {
		t.Fatalf("gamma create=%d %s", r.Code, r.Body.String())
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE stores SET vkn='9876543210' WHERE user_id=$1 AND id=$2", s.alice, gamma.Id); err != nil {
		t.Fatal(err)
	}
	r = s.call(s.alice, "POST", "/stores/"+created.Id.String()+"/merge", `{"sourceId":"`+gamma.Id.String()+`"}`)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "1234567890") || !strings.Contains(r.Body.String(), "9876543210") {
		t.Fatalf("tax number conflict=%d %s", r.Code, r.Body.String())
	}

	// Every operation on another user's store is a not-found.
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/stores/" + market.String(), ""},
		{"PUT", "/stores/" + market.String(), `{"name":"Forbidden"}`},
		{"POST", "/stores/" + market.String() + "/merge", `{"sourceId":"` + bd.Store.ID.String() + `"}`},
		{"DELETE", "/stores/" + market.String(), ""},
	} {
		if r := s.call(s.bob, tc.method, tc.path, tc.body); r.Code != 404 {
			t.Fatalf("foreign %s %s=%d", tc.method, tc.path, r.Code)
		}
	}
	r = s.call(s.bob, "GET", "/stores", "")
	var bobPage httpapi.StorePage
	if err := json.Unmarshal(r.Body.Bytes(), &bobPage); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || len(bobPage.Items) != 1 || bobPage.Items[0].Name != "Corner Shop" {
		t.Fatalf("second user's stores=%d %s", r.Code, r.Body.String())
	}

	// Delete takes a store without receipts, refuses the one holding
	// three naming the count, and reports a missing store.
	if r := s.call(s.alice, "DELETE", "/stores/"+gamma.Id.String(), ""); r.Code != 204 {
		t.Fatalf("delete empty=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.alice, "GET", "/stores/"+gamma.Id.String(), ""); r.Code != 404 {
		t.Fatalf("deleted detail=%d", r.Code)
	}
	r = s.call(s.alice, "DELETE", "/stores/"+market.String(), "")
	if r.Code != 409 || !strings.Contains(r.Body.String(), "3 receipts") {
		t.Fatalf("delete with receipts=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.alice, "DELETE", "/stores/"+uuid.NewString(), ""); r.Code != 404 {
		t.Fatalf("missing delete=%d", r.Code)
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
		{"GET", "/stores?limit=0", "", 400}, {"GET", "/stores?cursor=bad", "", 400}, {"GET", "/categories?limit=101", "", 400}, {"GET", "/categories?cursor=bad", "", 400}, {"POST", "/categories", `{"name":""}`, 400}, {"POST", "/categories", `{"name":"` + strings.Repeat("a", 101) + `"}`, 400}, {"POST", "/stores", `{"name":""}`, 400}, {"POST", "/stores", `{"name":"` + strings.Repeat("a", 201) + `"}`, 400}, {"PUT", "/stores/" + d.Store.ID.String(), `{"name":"!!!"}`, 400}, {"PUT", "/stores/" + uuid.NewString(), `{"name":"Missing"}`, 404}, {"GET", "/stores/" + uuid.NewString(), "", 404}, {"DELETE", "/stores/" + uuid.NewString(), "", 404}, {"POST", "/stores/" + uuid.NewString() + "/merge", `{"sourceId":"` + uuid.NewString() + `"}`, 404}, {"POST", "/stores/" + d.Store.ID.String() + "/merge", `{"sourceId":"` + d.Store.ID.String() + `"}`, 400}, {"POST", "/stores/" + d.Store.ID.String() + "/merge", `{"sourceId":"not-a-uuid"}`, 400}, {"PUT", "/categories/" + uuid.NewString(), `{"name":"Missing"}`, 404}, {"DELETE", "/categories/" + uuid.NewString(), "", 404}, {"PUT", "/settings", `{"defaultCurrency":"usd","defaultReceiptTitle":"A","llmSuggestionsEnabled":true}`, 400}, {"PUT", "/settings", `{"defaultCurrency":"TRY","defaultReceiptTitle":"","llmSuggestionsEnabled":true}`, 400},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			r := s.call(s.alice, tc.method, tc.path, tc.body)
			if r.Code != tc.status {
				t.Fatalf("status=%d want %d: %s", r.Code, tc.status, r.Body.String())
			}
		})
	}
}
