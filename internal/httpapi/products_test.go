package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/products"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type productServer struct {
	t          *testing.T
	id         func(sql string, args ...any) uuid.UUID
	exec       func(sql string, args ...any)
	http       *httpapi.Server
	sessions   *scs.SessionManager
	alice, bob uuid.UUID
}

func newProductServer(t *testing.T) *productServer {
	t.Helper()
	pool := dbtest.New(t)
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: "alice@example.com"}
	users := auth.New(pool, cfg, nil, time.Now)
	alice := testutil.NewUser(t, users).ID
	cfg.DevLoginEmail = "bob@example.com"
	bob := testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	sessions, stop := auth.NewSessions(pool, false)
	t.Cleanup(stop)
	now := func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }
	server := httpapi.New(":0", slog.New(slog.NewTextHandler(io.Discard, nil)), http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{}, nil, products.New(pool, now), nil, nil, nil)
	s := &productServer{t: t, http: server, sessions: sessions, alice: alice, bob: bob}
	s.id = func(sql string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(t.Context(), sql, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	s.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s *productServer) get(user uuid.UUID, path string) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.call(user, http.MethodGet, path, "")
}

func (s *productServer) call(user uuid.UUID, method, path, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	request := testutil.Request(s.t, s.sessions, user, method, "/api/v1"+path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	s.http.Handler().ServeHTTP(response, request)
	return response
}

// seedMilk stores a product of user with one alias, one store code and one
// purchase at a store, and returns the product id.
func (s *productServer) seedMilk(user uuid.UUID) uuid.UUID {
	s.t.Helper()
	store := s.id(`INSERT INTO stores (user_id, name, normalized_name) VALUES ($1, 'Synthetic Market', 'synthetic market') RETURNING id`, user)
	product := s.id(`INSERT INTO products (user_id, canonical_name, size_value, size_unit) VALUES ($1, 'Süt 1 L', 1, 'l') RETURNING id`, user)
	s.exec(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name, store_id, seen_count) VALUES ($1, $2, 'SUT 1L', 'sut 1000 ml', $3, 2)`, user, product, store)
	s.exec(`INSERT INTO product_codes (user_id, product_id, code, kind, store_id) VALUES ($1, $2, '004512', 'store', $3)`, user, product, store)
	receipt := s.id(`INSERT INTO receipts (user_id, title, store_id, purchased_on, currency, status) VALUES ($1, 'Synthetic receipt', $2, '2026-11-02', 'TRY', 'ready') RETURNING id`, user, store)
	s.exec(`INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, quantity, quantity_unit, unit_price, line_total, product_id, match_status)
		VALUES ($1, $2, 0, 'SUT 1L', 'SUT 1L', 1, 'AD', 42.25, 42.25, $3, 'auto_linked')`, user, receipt, product)
	return product
}

// decodeAs checks the status, validates the body against a schema of the
// contract and decodes it into T.
func decodeAs[T any](t *testing.T, response *httptest.ResponseRecorder, status int, schema string) T {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var document any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := loadContract(t).Components.Schemas[schema].Value.VisitJSON(document); err != nil {
		t.Fatalf("body violates %s: %v\n%s", schema, err, response.Body.String())
	}
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestProducts_ListAndGetFollowTheContract(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)

	page := decodeAs[httpapi.ProductPage](t, s.get(s.alice, "/products?q=sut&limit=10"), http.StatusOK, "ProductPage")
	if len(page.Items) != 1 || page.Items[0].Id != milk || page.NextCursor != nil {
		t.Fatalf("page = %+v", page)
	}
	summary := page.Items[0]
	if summary.PurchaseCount != 1 || summary.LastPurchase == nil || summary.LastPurchase.UnitPrice != "42.25" || summary.LastPurchase.Store == nil {
		t.Errorf("summary = %+v, last = %+v", summary, summary.LastPurchase)
	}

	product := decodeAs[httpapi.Product](t, s.get(s.alice, "/products/"+milk.String()), http.StatusOK, "Product")
	got := struct {
		Name, SizeValue, SizeUnit string
		Aliases, Codes            int
	}{product.CanonicalName, *product.SizeValue, *product.SizeUnit, len(product.Aliases), len(product.Codes)}
	want := struct {
		Name, SizeValue, SizeUnit string
		Aliases, Codes            int
	}{"Süt 1 L", "1.000", "l", 1, 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("product (-want +got):\n%s", diff)
	}
}

func TestProducts_RejectsBadListParameters(t *testing.T) {
	s := newProductServer(t)
	for name, query := range map[string]string{
		"limit zero":   "?limit=0",
		"empty search": "?q=",
		"bad cursor":   "?cursor=nonsense",
		"bad category": "?categoryId=not-a-uuid",
	} {
		t.Run(name, func(t *testing.T) {
			response := s.get(s.alice, "/products"+query)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			decodeProblem(t, response)
		})
	}
}

func TestProducts_IsolatesReadOperations(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)

	page := decodeAs[httpapi.ProductPage](t, s.get(s.bob, "/products"), http.StatusOK, "ProductPage")
	if len(page.Items) != 0 {
		t.Errorf("bob lists %+v", page.Items)
	}
	response := s.get(s.bob, "/products/"+milk.String())
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); strings.Contains(body, "Süt") || strings.Contains(body, "004512") {
		t.Errorf("problem leaks data: %s", body)
	}
	decodeProblem(t, response)
}

func TestProducts_UpdateFollowsTheContract(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	category := s.id(`INSERT INTO categories (user_id, name) VALUES ($1, 'Dairy') RETURNING id`, s.alice)

	body := `{"canonicalName":"Süt 2 L","categoryId":"` + category.String() + `","sizeValue":"1,5","sizeUnit":"l"}`
	product := decodeAs[httpapi.Product](t, s.call(s.alice, http.MethodPut, "/products/"+milk.String(), body), http.StatusOK, "Product")
	if product.CanonicalName != "Süt 2 L" || product.Category == nil || product.Category.Id != category || *product.SizeValue != "1.500" {
		t.Errorf("product = %+v", product)
	}

	for name, body := range map[string]string{
		"size without unit": `{"canonicalName":"Süt","sizeValue":"2"}`,
		"blank name":        `{"canonicalName":"   "}`,
		"bad size":          `{"canonicalName":"Süt","sizeValue":"two","sizeUnit":"l"}`,
		"unknown category":  `{"canonicalName":"Süt","categoryId":"` + uuid.NewString() + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := s.call(s.alice, http.MethodPut, "/products/"+milk.String(), body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			decodeProblem(t, response)
		})
	}
}

func TestProducts_SizeErrorsNameTheField(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	quantity := "enter a number with up to three decimal places, using . or , — such as 1.250"
	positive := "must be positive with at most three decimal places"
	for name, tc := range map[string]struct {
		body, message string
	}{
		"text":        {`{"canonicalName":"Süt","sizeValue":"two","sizeUnit":"l"}`, quantity},
		"zero":        {`{"canonicalName":"Süt","sizeValue":"0","sizeUnit":"g"}`, positive},
		"negative":    {`{"canonicalName":"Süt","sizeValue":"-1,5","sizeUnit":"g"}`, positive},
		"four places": {`{"canonicalName":"Süt","sizeValue":"1.2345","sizeUnit":"g"}`, quantity},
		"grouped":     {`{"canonicalName":"Süt","sizeValue":"1.234,56","sizeUnit":"l"}`, quantity},
	} {
		t.Run(name, func(t *testing.T) {
			response := s.call(s.alice, http.MethodPut, "/products/"+milk.String(), tc.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			problem := decodeProblem(t, response)
			if problem.Errors == nil || len(*problem.Errors) != 1 {
				t.Fatalf("problem has no single field error: %s", response.Body.String())
			}
			want := []httpapi.FieldProblem{{Field: "sizeValue", Message: tc.message}}
			if diff := cmp.Diff(want, *problem.Errors); diff != "" {
				t.Errorf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestProducts_IsolatesUpdate(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	response := s.call(s.bob, http.MethodPut, "/products/"+milk.String(), `{"canonicalName":"Taken"}`)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	decodeProblem(t, response)
	product := decodeAs[httpapi.Product](t, s.get(s.alice, "/products/"+milk.String()), http.StatusOK, "Product")
	if product.CanonicalName != "Süt 1 L" {
		t.Errorf("name = %q", product.CanonicalName)
	}
}

func TestProducts_PriceHistoryFollowsTheContract(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	history := decodeAs[httpapi.PriceHistory](t, s.get(s.alice, "/products/"+milk.String()+"/price-history?from=2026-01-01&to=2026-12-31"), http.StatusOK, "PriceHistory")
	if history.ProductId != milk || len(history.Points) != 1 || len(history.Stores) != 1 {
		t.Fatalf("history = %+v", history)
	}
	point, store := history.Points[0], history.Stores[0]
	got := []string{point.UnitPrice, *point.Quantity, *point.QuantityUnit, point.Currency, point.PurchasedOn.String(), store.Min, store.Max, store.Latest, store.LatestOn.String()}
	want := []string{"42.25", "1.000", "AD", "TRY", "2026-11-02", "42.25", "42.25", "42.25", "2026-11-02"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("history (-want +got):\n%s", diff)
	}

	for name, query := range map[string]string{
		"inverted range": "?from=2026-02-01&to=2026-01-01",
		"bad date":       "?from=yesterday",
		"bad store":      "?storeId=nope",
	} {
		t.Run(name, func(t *testing.T) {
			response := s.get(s.alice, "/products/"+milk.String()+"/price-history"+query)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			decodeProblem(t, response)
		})
	}
}

func TestProducts_IsolatesPriceHistory(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	response := s.get(s.bob, "/products/"+milk.String()+"/price-history")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "42.25") {
		t.Errorf("problem leaks data: %s", response.Body.String())
	}
	decodeProblem(t, response)
}

func TestProducts_PriceHistoryHugeBasisPriceStaysInContract(t *testing.T) {
	s := newProductServer(t)
	// A kg product bought as a thousandth of a kilogram at the largest
	// amount the API accepts: the per-kilogram price needs more integer
	// digits than any stored amount.
	cheese := s.id(`INSERT INTO products (user_id, canonical_name, price_basis) VALUES ($1, 'Cheese', 'kg') RETURNING id`, s.alice)
	receipt := s.id(`INSERT INTO receipts (user_id, title, purchased_on, total, currency, status) VALUES ($1, 'Synthetic receipt', '2026-11-02', 9999999999.99, 'TRY', 'ready') RETURNING id`, s.alice)
	s.exec(`INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, quantity, quantity_unit, unit_price, line_total, product_id, match_status)
		VALUES ($1, $2, 0, 'CHEESE', 'CHEESE', 0.001, 'KG', 9999999999.99, 9999999999.99, $3, 'auto_linked')`, s.alice, receipt, cheese)

	history := decodeAs[httpapi.PriceHistory](t, s.get(s.alice, "/products/"+cheese.String()+"/price-history"), http.StatusOK, "PriceHistory")

	if len(history.Points) != 1 {
		t.Fatalf("history = %+v", history.Points)
	}
	point := history.Points[0]
	if point.BasisPrice == nil || *point.BasisPrice != "9999999999990.00" {
		t.Errorf("basisPrice = %v, want the price of one kilogram", point.BasisPrice)
	}
}

func TestProducts_MergeFollowsTheContract(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	duplicate := s.id(`INSERT INTO products (user_id, canonical_name) VALUES ($1, 'Sut Litre') RETURNING id`, s.alice)
	s.exec(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name) VALUES ($1, $2, 'SUT LITRE', 'sut litre')`, s.alice, duplicate)

	product := decodeAs[httpapi.MergedProduct](t, s.call(s.alice, http.MethodPost, "/products/"+milk.String()+"/merge", `{"sourceProductId":"`+duplicate.String()+`"}`), http.StatusOK, "MergedProduct")
	if product.Id != milk || len(product.Aliases) != 2 {
		t.Errorf("product = %+v", product)
	}
	if response := s.get(s.alice, "/products/"+duplicate.String()); response.Code != http.StatusNotFound {
		t.Errorf("source still answers %d", response.Code)
	}

	response := s.call(s.alice, http.MethodPost, "/products/"+milk.String()+"/merge", `{"sourceProductId":"`+milk.String()+`"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("merge into itself: status = %d: %s", response.Code, response.Body.String())
	}
	decodeProblem(t, response)
}

func TestProducts_IsolatesMerge(t *testing.T) {
	s := newProductServer(t)
	alices := s.seedMilk(s.alice)
	bobs := s.seedMilk(s.bob)
	for name, tc := range map[string]struct{ target, source uuid.UUID }{
		"alice's as target": {alices, bobs},
		"alice's as source": {bobs, alices},
	} {
		t.Run(name, func(t *testing.T) {
			response := s.call(s.bob, http.MethodPost, "/products/"+tc.target.String()+"/merge", `{"sourceProductId":"`+tc.source.String()+`"}`)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			decodeProblem(t, response)
		})
	}
	for _, user := range []uuid.UUID{s.alice, s.bob} {
		page := decodeAs[httpapi.ProductPage](t, s.get(user, "/products"), http.StatusOK, "ProductPage")
		if len(page.Items) != 1 {
			t.Errorf("user %s has %d products", user, len(page.Items))
		}
	}
}

func TestProducts_SplitFollowsTheContract(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	alias := s.id(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name) VALUES ($1, $2, 'SUT 2L', 'sut 2000 ml') RETURNING id`, s.alice, milk)

	product := decodeAs[httpapi.Product](t, s.call(s.alice, http.MethodPost, "/products/"+milk.String()+"/split", `{"aliasIds":["`+alias.String()+`"],"canonicalName":"Süt 2 L"}`), http.StatusCreated, "Product")
	if product.Id == milk || product.CanonicalName != "Süt 2 L" || len(product.Aliases) != 1 || product.Aliases[0].Id != alias {
		t.Errorf("product = %+v", product)
	}

	for name, body := range map[string]string{
		"no aliases":    `{"aliasIds":[],"canonicalName":"New"}`,
		"foreign alias": `{"aliasIds":["` + uuid.NewString() + `"],"canonicalName":"New"}`,
		"blank name":    `{"aliasIds":["` + alias.String() + `"],"canonicalName":" "}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := s.call(s.alice, http.MethodPost, "/products/"+milk.String()+"/split", body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			decodeProblem(t, response)
		})
	}
}

func TestProducts_IsolatesSplit(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	alias := s.id(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name) VALUES ($1, $2, 'SUT 2L', 'sut 2000 ml') RETURNING id`, s.alice, milk)
	response := s.call(s.bob, http.MethodPost, "/products/"+milk.String()+"/split", `{"aliasIds":["`+alias.String()+`"],"canonicalName":"Taken"}`)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	decodeProblem(t, response)
	product := decodeAs[httpapi.Product](t, s.get(s.alice, "/products/"+milk.String()), http.StatusOK, "Product")
	if len(product.Aliases) != 2 {
		t.Errorf("aliases = %d, want 2", len(product.Aliases))
	}
}

func TestProducts_UpdateSetsThePriceBasis(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	product := decodeAs[httpapi.Product](t, s.call(s.alice, http.MethodPut, "/products/"+milk.String(), `{"canonicalName":"Süt","priceBasis":"l","sizeValue":"1","sizeUnit":"l"}`), http.StatusOK, "Product")
	if product.PriceBasis != httpapi.PriceBasis("l") || product.SizeValue != nil || product.SizeUnit != nil {
		t.Errorf("product = %+v", product)
	}
	response := s.call(s.alice, http.MethodPut, "/products/"+milk.String(), `{"canonicalName":"Süt","priceBasis":"box"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	decodeProblem(t, response)
}

func TestProducts_MergeReportsMixedSizes(t *testing.T) {
	s := newProductServer(t)
	milk := s.seedMilk(s.alice)
	bigger := s.id(`INSERT INTO products (user_id, canonical_name) VALUES ($1, 'Süt 2 L') RETURNING id`, s.alice)
	s.exec(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name) VALUES ($1, $2, 'SUT 2 LT', 'sut 2000 ml')`, s.alice, bigger)
	merged := decodeAs[httpapi.MergedProduct](t, s.call(s.alice, http.MethodPost, "/products/"+milk.String()+"/merge", `{"sourceProductId":"`+bigger.String()+`"}`), http.StatusOK, "MergedProduct")
	if merged.MixedSizes == nil || merged.MixedSizes.Basis != httpapi.PriceBasis("l") || strings.Join(merged.MixedSizes.Sizes, ",") != "1 l,2 l" {
		t.Errorf("mixed sizes = %+v", merged.MixedSizes)
	}
}
