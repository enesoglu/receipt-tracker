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
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/reports"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type reportServer struct {
	t          *testing.T
	exec       func(sql string, args ...any)
	http       *httpapi.Server
	sessions   *scs.SessionManager
	alice, bob uuid.UUID
}

func newReportServer(t *testing.T) *reportServer {
	t.Helper()
	pool := dbtest.New(t)
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: "alice@example.com"}
	users := auth.New(pool, cfg, nil, time.Now)
	alice := testutil.NewUser(t, users).ID
	cfg.DevLoginEmail = "bob@example.com"
	bob := testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	sessions, stop := auth.NewSessions(pool, false)
	t.Cleanup(stop)
	server := httpapi.New(":0", slog.New(slog.NewTextHandler(io.Discard, nil)), http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{}, reports.New(pool), nil, nil, nil, nil)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	return &reportServer{t: t, exec: exec, http: server, sessions: sessions, alice: alice, bob: bob}
}

func (s *reportServer) get(user uuid.UUID, path string) *httptest.ResponseRecorder {
	s.t.Helper()
	request := testutil.Request(s.t, s.sessions, user, http.MethodGet, "/api/v1"+path, strings.NewReader(""))
	response := httptest.NewRecorder()
	s.http.Handler().ServeHTTP(response, request)
	return response
}

func (s *reportServer) receipt(user uuid.UUID, date, total, currency, status string) {
	s.t.Helper()
	s.exec(`INSERT INTO receipts (user_id, title, purchased_on, total, currency, status) VALUES ($1, 'Synthetic receipt', NULLIF($2, '')::date, NULLIF($3, '')::numeric, $4, $5)`,
		user, date, total, currency, status)
}

func decodeReport[T any](t *testing.T, r *httptest.ResponseRecorder, schema string) T {
	t.Helper()
	if r.Code != http.StatusOK {
		t.Fatalf("status=%d want 200: %s", r.Code, r.Body.String())
	}
	var document interface{}
	if err := json.Unmarshal(r.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := loadContract(t).Components.Schemas[schema].Value.VisitJSON(document); err != nil {
		t.Fatalf("%s violates contract: %v", schema, err)
	}
	var report T
	if err := json.Unmarshal(r.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestGetMonthlySpending_ReturnsEveryMonthOfTheRange(t *testing.T) {
	s := newReportServer(t)
	s.receipt(s.alice, "2026-10-03", "10.50", "TRY", "ready")
	s.receipt(s.alice, "2026-10-04", "5.25", "TRY", "ready")
	s.receipt(s.alice, "2026-12-01", "23.80", "EUR", "needs_review")
	s.receipt(s.alice, "", "99.00", "TRY", "ready")

	got := decodeReport[httpapi.MonthlySpending](t, s.get(s.alice, "/reports/monthly-spending?from=2026-10&to=2026-12"), "MonthlySpending")

	want := httpapi.MonthlySpending{
		From: "2026-10", To: "2026-12", ExcludedReceipts: 1,
		Months: []httpapi.MonthSpending{
			{Month: "2026-10", Totals: []httpapi.CurrencyTotal{{Currency: "TRY", Amount: "15.75", ReceiptCount: 2}}},
			{Month: "2026-11", Totals: []httpapi.CurrencyTotal{}},
			{Month: "2026-12", Totals: []httpapi.CurrencyTotal{{Currency: "EUR", Amount: "23.80", ReceiptCount: 1}}},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("report mismatch (-want +got):\n%s", diff)
	}
}

func TestGetMonthlySpending_RejectsBadInput(t *testing.T) {
	s := newReportServer(t)
	for name, path := range map[string]string{
		"from after to": "/reports/monthly-spending?from=2026-12&to=2026-01",
		"more than 60":  "/reports/monthly-spending?from=2020-01&to=2026-01",
	} {
		t.Run(name, func(t *testing.T) {
			if r := s.get(s.alice, path); r.Code != http.StatusBadRequest {
				t.Errorf("status=%d want 400: %s", r.Code, r.Body.String())
			}
		})
	}
	for name, tc := range map[string]struct{ path, field string }{
		"bad from": {"/reports/monthly-spending?from=2026-13&to=2026-14", "from"},
		"bad to":   {"/reports/monthly-spending?from=2026-10&to=2026-13", "to"},
	} {
		t.Run(name, func(t *testing.T) {
			r := s.get(s.alice, tc.path)
			if r.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400: %s", r.Code, r.Body.String())
			}
			problem := decodeProblem(t, r)
			if problem.Errors == nil {
				t.Fatalf("problem names no field: %s", r.Body.String())
			}
			want := []httpapi.FieldProblem{{Field: tc.field, Message: "must be written as YYYY-MM"}}
			if diff := cmp.Diff(want, *problem.Errors); diff != "" {
				t.Errorf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetMonthlySpending_IsolatesUsers(t *testing.T) {
	s := newReportServer(t)
	s.receipt(s.alice, "2026-10-03", "10.50", "TRY", "ready")
	s.receipt(s.alice, "", "7.00", "TRY", "ready")

	got := decodeReport[httpapi.MonthlySpending](t, s.get(s.bob, "/reports/monthly-spending?from=2026-10&to=2026-10"), "MonthlySpending")

	if len(got.Months) != 1 || len(got.Months[0].Totals) != 0 || got.ExcludedReceipts != 0 {
		t.Errorf("user B saw user A's data: %+v", got)
	}
}

// breakdownFixture gives Alice one March receipt with a categorised line, a
// discount and a part the lines do not explain, and returns the receipt id.
func (s *reportServer) breakdownFixture() {
	s.t.Helper()
	s.exec(`WITH c AS (INSERT INTO categories (user_id, name) VALUES ($1, 'Dairy') RETURNING id),
	             p AS (INSERT INTO products (user_id, canonical_name, category_id) SELECT $1, 'Milk', id FROM c RETURNING id),
	             st AS (INSERT INTO stores (user_id, name, normalized_name) VALUES ($1, 'Market', 'market') RETURNING id),
	             r AS (INSERT INTO receipts (user_id, title, purchased_on, total, currency, status, store_id)
	                   SELECT $1, 'Synthetic receipt', '2026-03-10', 100.00, 'TRY', 'ready', id FROM st RETURNING id)
	        INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, line_total, product_id, match_status)
	        SELECT $1, r.id, v.position, 'ITEM', 'Item', v.total, CASE WHEN v.total > 0 THEN p.id END,
	               CASE WHEN v.total > 0 THEN 'confirmed' ELSE 'unmatched' END
	        FROM r, p, (VALUES (0, 80.00), (1, -10.00)) AS v(position, total)`, s.alice)
}

func TestGetMonthBreakdown_ByCategoryAddsUpToTheTotal(t *testing.T) {
	s := newReportServer(t)
	s.breakdownFixture()

	got := decodeReport[httpapi.MonthBreakdown](t, s.get(s.alice, "/reports/month-breakdown?month=2026-03&groupBy=category"), "MonthBreakdown")

	if len(got.Currencies) != 1 || got.Currencies[0].Total != "100.00" {
		t.Fatalf("currencies = %+v", got.Currencies)
	}
	type row struct{ Kind, Name, Amount, Share string }
	var rows []row
	for _, g := range got.Currencies[0].Groups {
		rows = append(rows, row{string(g.Kind), g.Name, g.Amount, g.Share})
	}
	want := []row{
		{"category", "Dairy", "80.00", "0.8000"},
		{"unallocated", "Unallocated", "30.00", "0.3000"},
		{"discounts", "Discounts", "-10.00", "-0.1000"},
	}
	if diff := cmp.Diff(want, rows); diff != "" {
		t.Errorf("groups mismatch (-want +got):\n%s", diff)
	}
	if got.Currencies[0].Groups[0].Id == nil {
		t.Error("category group has no id")
	}
}

func TestGetMonthBreakdown_ByStore(t *testing.T) {
	s := newReportServer(t)
	s.breakdownFixture()

	got := decodeReport[httpapi.MonthBreakdown](t, s.get(s.alice, "/reports/month-breakdown?month=2026-03&groupBy=store"), "MonthBreakdown")

	groups := got.Currencies[0].Groups
	if len(groups) != 1 || groups[0].Kind != httpapi.BreakdownGroupKind("store") || groups[0].Name != "Market" || groups[0].ItemCount != 1 || groups[0].Share != "1.0000" {
		t.Errorf("groups = %+v", groups)
	}
}

func TestGetMonthBreakdown_RejectsBadInput(t *testing.T) {
	s := newReportServer(t)
	for name, path := range map[string]string{
		"unknown grouping": "/reports/month-breakdown?month=2026-03&groupBy=colour",
	} {
		t.Run(name, func(t *testing.T) {
			if r := s.get(s.alice, path); r.Code != http.StatusBadRequest {
				t.Errorf("status=%d want 400: %s", r.Code, r.Body.String())
			}
		})
	}
	t.Run("malformed month", func(t *testing.T) {
		r := s.get(s.alice, "/reports/month-breakdown?month=2026-13&groupBy=store")
		if r.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400: %s", r.Code, r.Body.String())
		}
		problem := decodeProblem(t, r)
		if problem.Errors == nil {
			t.Fatalf("problem names no field: %s", r.Body.String())
		}
		want := []httpapi.FieldProblem{{Field: "month", Message: "must be written as YYYY-MM"}}
		if diff := cmp.Diff(want, *problem.Errors); diff != "" {
			t.Errorf("field errors mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestGetMonthBreakdown_IsolatesUsers(t *testing.T) {
	s := newReportServer(t)
	s.breakdownFixture()

	for _, by := range []string{"category", "store"} {
		got := decodeReport[httpapi.MonthBreakdown](t, s.get(s.bob, "/reports/month-breakdown?month=2026-03&groupBy="+by), "MonthBreakdown")
		if len(got.Currencies) != 0 || got.ExcludedReceipts != 0 {
			t.Errorf("%s: user B saw user A's data: %+v", by, got)
		}
	}
}

// hugeLinesWithNearZeroTotal stores two receipts whose totals cancel to one
// cent while each carries a line at the largest amount the API accepts, so
// every derived breakdown value leaves the range of a single stored amount.
func (s *reportServer) hugeLinesWithNearZeroTotal() {
	s.t.Helper()
	s.exec(`WITH c AS (INSERT INTO categories (user_id, name) VALUES ($1, 'Dairy') RETURNING id),
	             p AS (INSERT INTO products (user_id, canonical_name, category_id) SELECT $1, 'Milk', id FROM c RETURNING id),
	             r AS (INSERT INTO receipts (user_id, title, purchased_on, total, currency, status)
	                   SELECT $1, 'Synthetic receipt', '2026-03-10', 10.01, 'TRY', 'ready' RETURNING id)
	        INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, line_total, product_id, match_status)
	        SELECT $1, r.id, 0, 'MILK', 'MILK', 9999999999.99, p.id, 'confirmed' FROM r, p`, s.alice)
	s.exec(`WITH r AS (INSERT INTO receipts (user_id, title, purchased_on, total, currency, status)
	                   SELECT $1, 'Synthetic receipt', '2026-03-11', -10.00, 'TRY', 'ready' RETURNING id)
	        INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, line_total, match_status)
	        SELECT $1, r.id, 0, 'BREAD', 'BREAD', 9999999999.99, 'unmatched' FROM r`, s.alice)
}

func TestGetMonthlySpending_HugeTotalsStayInContract(t *testing.T) {
	s := newReportServer(t)
	s.receipt(s.alice, "2026-10-03", "9999999999.99", "TRY", "ready")
	s.receipt(s.alice, "2026-10-04", "9999999999.99", "TRY", "ready")

	got := decodeReport[httpapi.MonthlySpending](t, s.get(s.alice, "/reports/monthly-spending?from=2026-10&to=2026-10"), "MonthlySpending")

	if len(got.Months) != 1 || len(got.Months[0].Totals) != 1 || got.Months[0].Totals[0].ReceiptCount != 2 {
		t.Fatalf("months = %+v", got.Months)
	}
	if amount := got.Months[0].Totals[0].Amount; amount != "19999999999.98" {
		t.Errorf("amount = %q, want the sum of both totals", amount)
	}
}

func TestGetMonthBreakdown_HugeLinesWithNearZeroTotalStayInContract(t *testing.T) {
	s := newReportServer(t)
	s.hugeLinesWithNearZeroTotal()

	got := decodeReport[httpapi.MonthBreakdown](t, s.get(s.alice, "/reports/month-breakdown?month=2026-03&groupBy=category"), "MonthBreakdown")

	if len(got.Currencies) != 1 || got.Currencies[0].Total != "0.01" {
		t.Fatalf("currencies = %+v", got.Currencies)
	}
	type row struct{ Kind, Name, Amount, Share string }
	var rows []row
	var shares decimal.Decimal
	for _, g := range got.Currencies[0].Groups {
		rows = append(rows, row{string(g.Kind), g.Name, g.Amount, g.Share})
		share, err := decimal.NewFromString(g.Share)
		if err != nil {
			t.Fatalf("share %q: %v", g.Share, err)
		}
		shares = shares.Add(share)
	}
	want := []row{
		{"category", "Dairy", "9999999999.99", "999999999999.0000"},
		{"uncategorised", "Uncategorised", "9999999999.99", "999999999999.0000"},
		{"unallocated", "Unallocated", "-19999999999.97", "-1999999999997.0000"},
	}
	if diff := cmp.Diff(want, rows); diff != "" {
		t.Errorf("groups mismatch (-want +got):\n%s", diff)
	}
	if !shares.Equal(decimal.NewFromInt(1)) {
		t.Errorf("shares add up to %s, want 1", shares.StringFixed(4))
	}
}
