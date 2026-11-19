// Package mcpserver exposes only read operations through the official MCP SDK.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/oauthserver"
	"github.com/enesoglu/receipt-tracker/internal/products"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/reports"
	"github.com/enesoglu/receipt-tracker/internal/stores"
)

// Receipts contains no mutation methods, including for catalogue reads.
type Receipts interface {
	List(context.Context, uuid.UUID, receipts.Filter) (receipts.Page, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (receipts.Detail, error)
	ListCategories(context.Context, uuid.UUID, int, string) (receipts.NamedPage, error)
}

// Products contains only catalogue and price-history reads.
type Products interface {
	List(context.Context, uuid.UUID, products.Filter) (products.Page, error)
	PriceHistory(context.Context, uuid.UUID, uuid.UUID, products.HistoryFilter) (products.History, error)
}

// Stores contains only catalogue reads.
type Stores interface {
	List(context.Context, uuid.UUID, int, string) (stores.Page, error)
}

// Reports contains only spending aggregation.
type (
	Reports interface {
		MonthlySpending(context.Context, uuid.UUID, reports.Month, reports.Month) (reports.MonthlySpending, error)
	}
	// Tokens verifies the current admission and lifetime on every HTTP request.
	Tokens interface {
		VerifyAccess(context.Context, string) (oauthserver.Principal, error)
	}
)

// Options controls stream timing. NewTicker allows deterministic clock tests.
type Options struct {
	KeepAliveInterval time.Duration
	NewTicker         func(time.Duration) (<-chan time.Time, func())
}

// Handler owns an SDK server and its bearer-only HTTP transport.
type Handler struct {
	next     http.Handler
	logger   *slog.Logger
	receipts Receipts
	products Products
	stores   Stores
	reports  Reports
}

// New registers exactly seven read tools, binds each HTTP session to its user
// and accepts browser requests only from baseURL or the given browser origins.
func New(tokens Tokens, receiptService Receipts, productService Products, storeService Stores, reportService Reports, baseURL string, browserOrigins []string, logger *slog.Logger, options Options) *Handler {
	h := &Handler{logger: logger, receipts: receiptService, products: productService, stores: storeService, reports: reportService}
	server := mcp.NewServer(&mcp.Implementation{Name: "receipt-tracker", Version: "1.0.0"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}, SupportedProtocolVersions: []string{"2025-11-25", "2025-06-18"}})
	h.register(server)
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true, SessionTimeout: 15 * time.Minute, MaxRequestBodyBytes: 16 << 10})
	streams := keepalive(transport, options)
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != baseURL && !slices.Contains(browserOrigins, origin) {
			http.Error(w, "origin is not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		streams.ServeHTTP(w, r)
	})
	verifier := func(ctx context.Context, raw string, _ *http.Request) (*auth.TokenInfo, error) {
		principal, err := tokens.VerifyAccess(ctx, raw)
		if err != nil {
			var protocol *oauthserver.Error
			if errors.As(err, &protocol) {
				return nil, auth.ErrInvalidToken
			}
			h.logger.ErrorContext(ctx, "mcp token verification failed")
			return nil, errors.New("token verification unavailable")
		}
		scopes := strings.Fields(principal.Scope)
		if !slices.Contains(scopes, "receipts:read") {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: principal.UserID.String(), Scopes: scopes, Expiration: principal.ExpiresAt}, nil
	}
	// Clients prefer challenge scopes to metadata scopes. Do not narrow the
	// challenge to receipts:read: discovery also advertises offline_access so
	// normal connectors can request rotating refresh tokens. The verifier above
	// still enforces the read scope on every access token.
	h.next = auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{ResourceMetadataURL: baseURL + "/.well-known/oauth-protected-resource/mcp"})(protected)
	return h
}

// ServeHTTP uses authorization headers exclusively; it never loads app sessions.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.next.ServeHTTP(w, r) }

const maxResultBytes = 64 << 10

type (
	paging struct {
		Limit  int    `json:"limit,omitempty"`
		Cursor string `json:"cursor,omitempty"`
	}
	receiptInput struct {
		paging
		Query   string `json:"query,omitempty"`
		Month   string `json:"month,omitempty"`
		Status  string `json:"status,omitempty"`
		StoreID string `json:"store_id,omitempty"`
	}
	detailInput struct {
		paging
		ReceiptID string `json:"receipt_id"`
	}
	productInput struct {
		paging
		Query      string `json:"query,omitempty"`
		CategoryID string `json:"category_id,omitempty"`
	}
	historyInput struct {
		paging
		ProductID   string `json:"product_id"`
		From        string `json:"from,omitempty"`
		To          string `json:"to,omitempty"`
		StoreID     string `json:"store_id,omitempty"`
		StoreCursor string `json:"store_cursor,omitempty"`
	}
	spendingInput struct {
		paging
		From string `json:"from"`
		To   string `json:"to"`
	}
)

func str(description string, maxLength int) map[string]any {
	return map[string]any{"type": "string", "description": description, "maxLength": maxLength}
}

func identifier(description string) map[string]any {
	return map[string]any{"type": "string", "format": "uuid", "description": description}
}

func schema(properties map[string]any, required ...string) map[string]any {
	properties["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 20, "description": "Maximum items per page (1-50). Reduce it for large receipts or reports."}
	properties["cursor"] = str("Opaque next_cursor from the same tool, user and filters.", 2048)
	result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func register[I any](h *Handler, s *mcp.Server, name, description string, input map[string]any, fn func(context.Context, uuid.UUID, I) (any, error)) {
	no := false
	mcp.AddTool[I, any](s, &mcp.Tool{Name: name, Description: description, InputSchema: input, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &no, OpenWorldHint: &no}}, func(ctx context.Context, req *mcp.CallToolRequest, in I) (*mcp.CallToolResult, any, error) {
		if req.Extra == nil || req.Extra.TokenInfo == nil {
			return toolError(401, "authentication required"), nil, nil
		}
		userID, err := uuid.Parse(req.Extra.TokenInfo.UserID)
		if err != nil || userID == uuid.Nil {
			return toolError(401, "authentication required"), nil, nil
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		value, err := fn(ctx, userID, in)
		if err != nil {
			switch {
			case errors.Is(err, apperr.NotFound):
				return toolError(404, "resource not found"), nil, nil
			case errors.Is(err, apperr.Invalid):
				return toolError(400, "invalid arguments; check ids, dates, filters and cursor"), nil, nil
			default:
				h.logger.ErrorContext(ctx, "mcp tool failed", "tool", name)
				return toolError(500, "could not read data"), nil, nil
			}
		}
		result, err := bounded(value)
		if err != nil {
			return toolError(413, "result too large; reduce limit or narrow the date range"), nil, nil
		}
		return result, nil, nil
	})
}

func toolError(status int, message string) *mcp.CallToolResult {
	data, _ := json.Marshal(map[string]any{"status": status, "error": message})
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func bounded(value any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxResultBytes {
		return nil, fmt.Errorf("tool output exceeds %d bytes", maxResultBytes)
	}
	return result, nil
}

func (h *Handler) register(s *mcp.Server) {
	register(h, s, "list_receipts", "List your receipts, optionally filtered by month, status, store or text. Returns a cursor page; amounts are decimal strings with currency.", schema(map[string]any{"query": str("Receipt title or store search.", 100), "month": str("Purchase month, YYYY-MM.", 7), "status": str("queued, processing, needs_review, ready, failed or waiting_quota.", 20), "store_id": identifier("Filter to one of your stores.")}), h.listReceipts)
	register(h, s, "get_receipt", "Get one of your receipts and a page of its line items. Follow next_cursor for more lines. No images or storage credentials are exposed.", schema(map[string]any{"receipt_id": identifier("Receipt to read.")}, "receipt_id"), h.getReceipt)
	register(h, s, "search_products", "Search your product catalogue by canonical name or a name seen on a receipt. Returns a cursor page, with the last purchase's price and currency.", schema(map[string]any{"query": str("Product-name search; omit to browse all products.", 100), "category_id": identifier("Filter to one of your categories.")}), h.searchProducts)
	register(h, s, "get_price_history", "Read prices paid for one of your products across stores. Points and store summaries are paginated independently. Prices are decimal strings with currency; kg/l basis prices support comparing pack sizes.", schema(map[string]any{"product_id": identifier("Product whose purchases to read."), "from": str("Inclusive start date, YYYY-MM-DD.", 10), "to": str("Inclusive end date, YYYY-MM-DD.", 10), "store_id": identifier("Filter to one store."), "store_cursor": str("Opaque next_store_cursor for store summaries with the same filters.", 2048)}, "product_id"), h.priceHistory)
	register(h, s, "get_spending_summary", "Read monthly spending in an inclusive YYYY-MM range (at most 60 months). Each currency remains separate. Returns a page of months and the count excluded for missing date or total.", schema(map[string]any{"from": str("First month, YYYY-MM.", 7), "to": str("Last month, YYYY-MM.", 7)}, "from", "to"), h.spendingSummary)
	register(h, s, "list_categories", "List a page of your categories and their product counts.", schema(map[string]any{}), h.listCategories)
	register(h, s, "list_stores", "List a page of your stores and their receipt counts.", schema(map[string]any{}), h.listStores)
}
