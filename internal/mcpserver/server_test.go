package mcpserver_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/mcpserver"
	"github.com/enesoglu/receipt-tracker/internal/oauthserver"
	"github.com/enesoglu/receipt-tracker/internal/products"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/reports"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type switcher struct {
	mu      sync.RWMutex
	handler http.Handler
}

func (s *switcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.handler
	s.mu.RUnlock()
	h.ServeHTTP(w, r)
}
func (s *switcher) set(h http.Handler) { s.mu.Lock(); s.handler = h; s.mu.Unlock() }

type fixture struct {
	pool                                           *pgxpool.Pool
	server                                         *httptest.Server
	client                                         *http.Client
	sessions                                       *scs.SessionManager
	oauth                                          *oauthserver.Service
	gate                                           switcher
	logger                                         *slog.Logger
	logs                                           bytes.Buffer
	receipts                                       *receipts.Service
	products                                       *products.Service
	reports                                        *reports.Service
	user, other, receipt, product, store, category uuid.UUID
	tokenClients                                   map[string]uuid.UUID
	base                                           string
	options                                        mcpserver.Options
}

func setup(t *testing.T, options mcpserver.Options) *fixture {
	t.Helper()
	f := &fixture{pool: dbtest.New(t), options: options, tokenClients: make(map[string]uuid.UUID)}
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.test", "bob@example.test"}, DevLoginEmail: "alice@example.test"}
	users := auth.New(f.pool, cfg, nil, time.Now)
	f.user = testutil.NewUser(t, users).ID
	cfg.DevLoginEmail = "bob@example.test"
	f.other = testutil.NewUser(t, auth.New(f.pool, cfg, nil, time.Now)).ID
	var stop func()
	f.sessions, stop = auth.NewSessions(f.pool, false)
	t.Cleanup(stop)
	f.server = httptest.NewUnstartedServer(http.NotFoundHandler())
	f.base = "http://" + f.server.Listener.Addr().String()
	f.oauth = oauthserver.New(f.pool, users, f.base, time.Now)
	f.logger = slog.New(slog.NewJSONHandler(&f.logs, nil))
	f.receipts = receipts.New(f.pool, time.Now)
	f.products = products.New(f.pool, time.Now)
	f.reports = reports.New(f.pool)
	f.reset()
	oauth, err := oauthserver.NewHandler(f.oauth, f.sessions, f.logger)
	if err != nil {
		t.Fatal(err)
	}
	f.server.Config.Handler = httpapi.New(":0", f.logger, http.NotFoundHandler(), users, f.sessions, nil, httpapi.Ingestion{}, f.reports, f.products, oauth, &f.gate, f.receipts).Handler()
	f.server.Config.WriteTimeout = 30 * time.Second
	f.server.Start()
	t.Cleanup(f.server.Close)
	f.client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	f.store = uuid.New()
	f.category = uuid.New()
	f.receipt = uuid.New()
	f.product = uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO stores(id,user_id,name,normalized_name) VALUES($1,$2,'Alice store','alice store')", []any{f.store, f.user}},
		{"INSERT INTO categories(id,user_id,name) VALUES($1,$2,'Alice category')", []any{f.category, f.user}},
		{"INSERT INTO products(id,user_id,canonical_name,category_id) VALUES($1,$2,'Alice product',$3)", []any{f.product, f.user, f.category}},
		{"INSERT INTO receipts(id,user_id,title,store_id,purchased_on,currency,total,status) VALUES($1,$2,'Alice receipt',$3,'2026-11-01','TRY',12.50,'ready')", []any{f.receipt, f.user, f.store}},
		{"INSERT INTO line_items(user_id,receipt_id,position,raw_name,name,quantity,unit_price,line_total,product_id,match_status) SELECT $1,$2,n,'Alice product','Alice product',1,12.50,12.50,$3,'confirmed' FROM generate_series(1,3) n", []any{f.user, f.receipt, f.product}},
	} {
		if _, err := f.pool.Exec(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) reset() {
	f.gate.set(mcpserver.New(f.oauth, f.receipts, f.products, f.reports, f.base, f.logger, f.options))
}

type testResponse struct {
	StatusCode int
	Header     http.Header
	data       []byte
}

func (f *fixture) request(t *testing.T, method, path, body, content, token, session string, cookie *http.Cookie) *testResponse {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, f.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if session != "" {
		r.Header.Set("Mcp-Session-Id", session)
	}
	if content != "" {
		r.Header.Set("Content-Type", content)
	}
	if method == "POST" {
		r.Header.Set("Origin", f.base)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	res, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return &testResponse{StatusCode: res.StatusCode, Header: res.Header, data: data}
}

func read(t *testing.T, res *testResponse, want int) []byte {
	t.Helper()
	b := res.data
	if res.StatusCode != want {
		t.Fatalf("status=%d want=%d body=%s", res.StatusCode, want, b)
	}
	return b
}

func (f *fixture) tokens(t *testing.T, user uuid.UUID) oauthserver.Tokens {
	t.Helper()
	challenge := f.request(t, "POST", "/mcp", "{}", "application/json", "", "", nil)
	read(t, challenge, 401)
	if strings.Contains(challenge.Header.Get("WWW-Authenticate"), "scope=") {
		t.Fatal("challenge narrows metadata scopes and prevents offline access")
	}
	res := f.request(t, "GET", "/.well-known/oauth-protected-resource/mcp", "", "", "", "", nil)
	var metadata struct {
		Scopes []string `json:"scopes_supported"`
	}
	if err := json.Unmarshal(read(t, res, 200), &metadata); err != nil {
		t.Fatal(err)
	}
	read(t, f.request(t, "GET", "/.well-known/oauth-authorization-server", "", "", "", "", nil), 200)
	body := `{"client_name":"Synthetic MCP Inspector","redirect_uris":["` + f.base + `/callback"]}`
	var registered struct {
		ID uuid.UUID `json:"client_id"`
	}
	if err := json.Unmarshal(read(t, f.request(t, "POST", "/oauth/register", body, "application/json", "", "", nil), 201), &registered); err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	p := url.Values{"client_id": {registered.ID.String()}, "redirect_uri": {f.base + "/callback"}, "response_type": {"code"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {f.base + "/mcp"}, "scope": {strings.Join(metadata.Scopes, " ")}}
	cookie := testutil.Request(t, f.sessions, user, "GET", f.base, nil).Cookies()[0]
	page := read(t, f.request(t, "GET", "/oauth/authorize?"+p.Encode(), "", "", "", "", cookie), 200)
	match := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatal("missing consent")
	}
	p.Set("csrf", string(match[1]))
	p.Set("decision", "approve")
	res = f.request(t, "POST", "/oauth/authorize", p.Encode(), "application/x-www-form-urlencoded", "", "", cookie)
	read(t, res, 303)
	target, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	p = url.Values{"grant_type": {"authorization_code"}, "client_id": {registered.ID.String()}, "redirect_uri": {f.base + "/callback"}, "code_verifier": {verifier}, "resource": {f.base + "/mcp"}, "code": {target.Query().Get("code")}}
	var tokens oauthserver.Tokens
	if err := json.Unmarshal(read(t, f.request(t, "POST", "/oauth/token", p.Encode(), "application/x-www-form-urlencoded", "", "", nil), 200), &tokens); err != nil {
		t.Fatal(err)
	}
	f.tokenClients[tokens.RefreshToken] = registered.ID
	return tokens
}

func (f *fixture) rpc(t *testing.T, token, session, method string, params any) (json.RawMessage, *testResponse) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	res := f.request(t, "POST", "/mcp", string(body), "application/json", token, session, nil)
	data := read(t, res, 200)
	var wire struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Error) > 0 {
		t.Fatalf("rpc error: %s", wire.Error)
	}
	return wire.Result, res
}

func (f *fixture) initialize(t *testing.T, token string) string {
	t.Helper()
	data, res := f.rpc(t, token, "", "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "scripted-client", "version": "1"}})
	var initialized mcp.InitializeResult
	if err := json.Unmarshal(data, &initialized); err != nil {
		t.Fatal(err)
	}
	if initialized.Capabilities.Prompts != nil || initialized.Capabilities.Resources != nil {
		t.Fatal("unexpected capabilities")
	}
	session := res.Header.Get("Mcp-Session-Id")
	if session == "" {
		t.Fatal("missing session id")
	}
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	read(t, f.request(t, "POST", "/mcp", body, "application/json", token, session, nil), 202)
	return session
}

func (f *fixture) call(t *testing.T, token, session, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	data, _ := f.rpc(t, token, session, "tools/call", map[string]any{"name": name, "arguments": args})
	if len(data) > 70<<10 {
		t.Fatalf("%s output too large: %d", name, len(data))
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func text(result *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range result.Content {
		if v, ok := content.(*mcp.TextContent); ok {
			b.WriteString(v.Text)
		}
	}
	return b.String()
}

func tools(f *fixture) map[string]map[string]any {
	return map[string]map[string]any{"list_receipts": {}, "get_receipt": {"receipt_id": f.receipt.String()}, "search_products": {}, "get_price_history": {"product_id": f.product.String()}, "get_spending_summary": {"from": "2026-11", "to": "2026-11"}, "list_categories": {}, "list_stores": {}}
}

func TestMCP_FullOAuthToolRefreshFlowAndReadOnlyInventory(t *testing.T) {
	f := setup(t, mcpserver.Options{})
	tokens := f.tokens(t, f.user)
	session := f.initialize(t, tokens.AccessToken)
	data, _ := f.rpc(t, tokens.AccessToken, session, "tools/list", map[string]any{})
	var list mcp.ListToolsResult
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if required, exists := schema["required"]; exists && required == nil {
			t.Fatal("required must be an array when present; TypeScript clients reject null")
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Description == "" {
			t.Fatal("missing read-only contract")
		}
	}
	expected := map[string]bool{}
	for name := range tools(f) {
		expected[name] = true
	}
	if diff := cmp.Diff(expected, names); diff != "" {
		t.Fatal(diff)
	}
	snapshot := func() []int64 {
		var counts [6]int64
		err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM receipts),(SELECT count(*) FROM products),(SELECT count(*) FROM stores),(SELECT count(*) FROM categories),(SELECT count(*) FROM line_items),(SELECT count(*) FROM audit_events)").Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5])
		if err != nil {
			t.Fatal(err)
		}
		return counts[:]
	}
	before := snapshot()
	for name, args := range tools(f) {
		t.Run(name, func(t *testing.T) {
			result := f.call(t, tokens.AccessToken, session, name, args)
			if result.IsError {
				t.Fatalf("%s", text(result))
			}
			if name == "get_receipt" || name == "get_price_history" || name == "get_spending_summary" {
				if !strings.Contains(text(result), `"TRY"`) || !strings.Contains(text(result), `"12.50"`) {
					t.Fatal("money lost decimal string or currency")
				}
			}
		})
	}
	if diff := cmp.Diff(before, snapshot()); diff != "" {
		t.Fatal("read tools modified data: " + diff)
	}
	p := url.Values{"grant_type": {"refresh_token"}, "client_id": {f.tokenClients[tokens.RefreshToken].String()}, "resource": {f.base + "/mcp"}, "refresh_token": {tokens.RefreshToken}}
	var rotated oauthserver.Tokens
	if err := json.Unmarshal(read(t, f.request(t, "POST", "/oauth/token", p.Encode(), "application/x-www-form-urlencoded", "", "", nil), 200), &rotated); err != nil {
		t.Fatal(err)
	}
	if f.call(t, rotated.AccessToken, session, "list_receipts", map[string]any{}).IsError {
		t.Fatal("refreshed token failed in existing session")
	}
	for _, secret := range []string{tokens.AccessToken, tokens.RefreshToken, rotated.AccessToken, rotated.RefreshToken} {
		if strings.Contains(f.logs.String(), secret) {
			t.Fatal("credential leaked in logs")
		}
	}
}

func TestMCP_EveryToolIsolatesUsers(t *testing.T) {
	f := setup(t, mcpserver.Options{})
	a := f.tokens(t, f.user)
	b := f.tokens(t, f.other)
	aSession := f.initialize(t, a.AccessToken)
	bSession := f.initialize(t, b.AccessToken)
	for name, args := range tools(f) {
		t.Run(name, func(t *testing.T) {
			own := f.call(t, a.AccessToken, aSession, name, args)
			if own.IsError {
				t.Fatal(text(own))
			}
			other := f.call(t, b.AccessToken, bSession, name, args)
			if name == "get_receipt" || name == "get_price_history" {
				if !other.IsError || !strings.Contains(text(other), `"status":404`) {
					t.Fatal("foreign resource did not return not found")
				}
			} else if other.IsError {
				t.Fatal(text(other))
			}
			if strings.Contains(text(other), "Alice") || strings.Contains(text(other), f.receipt.String()) || strings.Contains(text(other), f.product.String()) || strings.Contains(text(other), f.category.String()) || strings.Contains(text(other), `"12.50"`) {
				t.Fatal("foreign data leaked")
			}
		})
	}
	res := f.request(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "application/json", b.AccessToken, aSession, nil)
	read(t, res, 403)
}

func TestMCP_BearerOnlyMetadataChallengeAndNoRESTAuthority(t *testing.T) {
	f := setup(t, mcpserver.Options{})
	tokens := f.tokens(t, f.user)
	expected := "Bearer resource_metadata=\"" + f.base + "/.well-known/oauth-protected-resource/mcp\""
	for _, raw := range []string{"", "invalid", tokens.RefreshToken} {
		res := f.request(t, "POST", "/mcp", "{}", "application/json", raw, "", nil)
		if res.Header.Get("WWW-Authenticate") != expected {
			t.Fatal("incorrect discovery challenge")
		}
		read(t, res, 401)
	}
	cookie := testutil.Request(t, f.sessions, f.user, "GET", f.base, nil).Cookies()[0]
	read(t, f.request(t, "POST", "/mcp", "{}", "application/json", "", "", cookie), 401)
	read(t, f.request(t, "GET", "/api/v1/me", "", "", tokens.AccessToken, "", nil), 401)
	wrongResource := oauthserver.New(f.pool, auth.New(f.pool, config.Config{AllowedEmails: []string{"alice@example.test"}}, nil, time.Now), f.base+"/other", time.Now)
	f.gate.set(mcpserver.New(wrongResource, f.receipts, f.products, f.reports, f.base, f.logger, mcpserver.Options{}))
	read(t, f.request(t, "POST", "/mcp", "{}", "application/json", tokens.AccessToken, "", nil), 401)
}

func TestMCP_PaginatesAndBindsCursorsToUserResourceAndFilters(t *testing.T) {
	f := setup(t, mcpserver.Options{})
	tokens := f.tokens(t, f.user)
	session := f.initialize(t, tokens.AccessToken)
	for _, name := range []string{"get_receipt", "get_price_history", "get_spending_summary"} {
		args := tools(f)[name]
		args["limit"] = 1
		if name == "get_spending_summary" {
			args["to"] = "2026-12"
		}
		first := f.call(t, tokens.AccessToken, session, name, args)
		if first.IsError {
			t.Fatal(text(first))
		}
		var page struct {
			Next string `json:"next_cursor"`
		}
		if err := json.Unmarshal([]byte(text(first)), &page); err != nil {
			t.Fatal(err)
		}
		if page.Next == "" {
			t.Fatal("missing pagination cursor for " + name)
		}
		args["cursor"] = page.Next
		if result := f.call(t, tokens.AccessToken, session, name, args); result.IsError {
			t.Fatal(text(result))
		}
		args["cursor"] = page.Next + "invalid"
		if result := f.call(t, tokens.AccessToken, session, name, args); !result.IsError {
			t.Fatal("malformed cursor accepted")
		}
	}
	if result := f.call(t, tokens.AccessToken, session, "list_receipts", map[string]any{"limit": 51}); !result.IsError {
		t.Fatal("unbounded limit accepted")
	}
}

func TestMCP_IdleKeepaliveReconnectAndForgottenSession(t *testing.T) {
	ticks := make(chan time.Time, 4)
	created := make(chan time.Duration, 4)
	options := mcpserver.Options{NewTicker: func(d time.Duration) (<-chan time.Time, func()) { created <- d; return ticks, func() {} }}
	f := setup(t, options)
	tokens := f.tokens(t, f.user)
	session := f.initialize(t, tokens.AccessToken)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	open := func() *http.Response {
		r, err := http.NewRequestWithContext(ctx, "GET", f.base+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		r.Header.Set("Accept", "text/event-stream")
		r.Header.Set("Mcp-Session-Id", session)
		r.Header.Set("Mcp-Protocol-Version", "2025-11-25")
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
		return res
	}
	res := open()
	reader := bufio.NewReader(res.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, ":") {
		t.Fatal("no initial stream comment")
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-created:
		if d != 25*time.Second {
			t.Fatal("incorrect default interval")
		}
	case <-time.After(time.Second):
		t.Fatal("ticker not created")
	}
	ticks <- time.Now()
	line, err = reader.ReadString('\n')
	if err != nil || line != ": keepalive\n" {
		t.Fatalf("keepalive=%q err=%v", line, err)
	}
	f.server.CloseClientConnections()
	_ = res.Body.Close()
	if f.call(t, tokens.AccessToken, session, "list_receipts", map[string]any{}).IsError {
		t.Fatal("tool call depended on stream")
	}
	res = open()
	_ = res.Body.Close()
	f.reset()
	read(t, f.request(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, "application/json", tokens.AccessToken, session, nil), 404)
	session = f.initialize(t, tokens.AccessToken)
	if f.call(t, tokens.AccessToken, session, "list_receipts", map[string]any{}).IsError {
		t.Fatal("reinitialization needed a new sign-in")
	}
}

// TestInspectorHarness supplies synthetic data to the separately invoked local
// Inspector. Its opt-in file contains temporary credentials and is never logged.
func TestInspectorHarness(t *testing.T) {
	const path = "../../bin/inspector-probe.json"
	if os.Getenv("MCP_INSPECTOR_PROBE") == "" {
		t.Skip("manual MCP Inspector harness")
	}
	f := setup(t, mcpserver.Options{})
	tokens := f.tokens(t, f.user)
	payload, err := json.Marshal(map[string]any{"url": f.base + "/mcp", "token": tokens.AccessToken, "tools": tools(f)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(10 * time.Minute)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(path + ".stop"); err == nil {
				return
			}
		case <-timeout.C:
			t.Fatal("Inspector exercise timed out")
		case <-t.Context().Done():
			return
		}
	}
}

// Oversized fakes are deliberately outside domain limits, guarding the final
// transport boundary even if an imported record is unexpectedly large.
type hugeReceipts struct {
	*receipts.Service
	big string
}

func (h hugeReceipts) List(context.Context, uuid.UUID, receipts.Filter) (receipts.Page, error) {
	return receipts.Page{Items: []receipts.Summary{{Receipt: sqlc.Receipt{Title: h.big}}}}, nil
}

func (h hugeReceipts) Get(context.Context, uuid.UUID, uuid.UUID) (receipts.Detail, error) {
	return receipts.Detail{Summary: receipts.Summary{Receipt: sqlc.Receipt{Title: h.big}}}, nil
}

func (h hugeReceipts) ListCategories(context.Context, uuid.UUID, int, string) (receipts.NamedPage, error) {
	return receipts.NamedPage{Items: []receipts.Named{{Name: h.big}}}, nil
}

func (h hugeReceipts) ListStores(context.Context, uuid.UUID, int, string) (receipts.NamedPage, error) {
	return receipts.NamedPage{Items: []receipts.Named{{Name: h.big}}}, nil
}

type hugeProducts struct {
	*products.Service
	big string
}

func (h hugeProducts) List(context.Context, uuid.UUID, products.Filter) (products.Page, error) {
	return products.Page{Items: []products.Summary{{Product: sqlc.Product{CanonicalName: h.big}}}}, nil
}

func (h hugeProducts) PriceHistory(context.Context, uuid.UUID, uuid.UUID, products.HistoryFilter) (products.History, error) {
	return products.History{Points: []products.PricePoint{{Currency: h.big, UnitPrice: decimal.NewFromInt(1)}}}, nil
}

type hugeReports struct{ big string }

func (h hugeReports) MonthlySpending(_ context.Context, _ uuid.UUID, from, to reports.Month) (reports.MonthlySpending, error) {
	return reports.MonthlySpending{From: from, To: to, Months: []reports.MonthSpending{{Month: from, Totals: []reports.CurrencyTotal{{Currency: h.big}}}}}, nil
}

func TestMCP_EveryToolBoundsItsSerializedOutput(t *testing.T) {
	f := setup(t, mcpserver.Options{})
	big := strings.Repeat("oversized synthetic data ", 10000)
	f.gate.set(mcpserver.New(f.oauth, hugeReceipts{f.receipts, big}, hugeProducts{f.products, big}, hugeReports{big}, f.base, f.logger, mcpserver.Options{}))
	tokens := f.tokens(t, f.user)
	session := f.initialize(t, tokens.AccessToken)
	for name, args := range tools(f) {
		t.Run(name, func(t *testing.T) {
			result := f.call(t, tokens.AccessToken, session, name, args)
			if !result.IsError || !strings.Contains(text(result), `"status":413`) {
				t.Fatal("oversized output was not bounded")
			}
			if len(text(result)) > 1000 {
				t.Fatal("oversized error response")
			}
		})
	}
}

type unavailableTokens struct{}

func (unavailableTokens) VerifyAccess(context.Context, string) (oauthserver.Principal, error) {
	return oauthserver.Principal{}, errors.New("synthetic private database detail")
}

func TestMCP_UnexpectedAuthFailureIsGenericAndProtocolContractIsValid(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := mcpserver.New(unavailableTokens{}, nil, nil, nil, "https://example.test", logger, mcpserver.Options{})
	req := httptest.NewRequestWithContext(t.Context(), "POST", "https://example.test/mcp", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer synthetic")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 500 || strings.Contains(res.Body.String(), "private database") {
		t.Fatal("unexpected authentication error exposed internal details")
	}
	contract, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type scopeTokens struct{ scope string }

func (s scopeTokens) VerifyAccess(context.Context, string) (oauthserver.Principal, error) {
	return oauthserver.Principal{UserID: uuid.New(), Scope: s.scope, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestMCP_ReadScopeIsEnforcedWithoutNarrowingDiscovery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := mcpserver.New(scopeTokens{"offline_access"}, nil, nil, nil, "https://example.test", logger, mcpserver.Options{})
	request := httptest.NewRequestWithContext(t.Context(), "POST", "https://example.test/mcp", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer synthetic")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 || strings.Contains(response.Header().Get("WWW-Authenticate"), "scope=") {
		t.Fatal("read scope enforcement or discovery challenge is incorrect")
	}
}
