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
	server := httpapi.New(":0", slog.New(slog.NewTextHandler(io.Discard, nil)), http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{}, nil, products.New(pool, now))
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

	body := `{"canonicalName":"Süt 2 L","categoryId":"` + category.String() + `","sizeValue":"2","sizeUnit":"l"}`
	product := decodeAs[httpapi.Product](t, s.call(s.alice, http.MethodPut, "/products/"+milk.String(), body), http.StatusOK, "Product")
	if product.CanonicalName != "Süt 2 L" || product.Category == nil || product.Category.Id != category || *product.SizeValue != "2.000" {
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
