package httpapi

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/products"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

// TestResponseTimestampsAreUTC guards the API contract that audit timestamps
// are RFC 3339 in UTC. The values are built with a fixed non-UTC offset so the
// serialization boundary, not the database codec (see db.Open), is exercised.
func TestResponseTimestampsAreUTC(t *testing.T) {
	offset := time.FixedZone("UTC+3", 3*60*60)
	created := time.Date(2026, 11, 20, 0, 30, 0, 0, offset)
	updated := time.Date(2026, 11, 20, 1, 15, 0, 0, offset)

	receipt := sqlc.Receipt{
		ID: uuid.New(), Title: "Synthetic receipt", Currency: "TRY",
		Status: "needs_review", Source: "web",
		CreatedAt: created, UpdatedAt: updated,
	}
	flag := sqlc.ReviewFlag{
		ID: uuid.New(), ReceiptID: receipt.ID, Kind: "sum_mismatch",
		Detail: []byte(`{}`), CreatedAt: created,
	}
	resolved := updated
	flag.ResolvedAt = &resolved

	product := sqlc.Product{
		ID: uuid.New(), CanonicalName: "Synthetic product",
		CreatedAt: created, UpdatedAt: updated,
	}
	alias := products.Alias{ProductAlias: sqlc.ProductAlias{
		ID: uuid.New(), ProductID: product.ID, RawName: "synthetic",
		NormalizedName: "synthetic", SeenCount: 2,
		CreatedAt: created, LastSeenAt: created,
	}}

	detail, err := receiptResponse(receipts.Detail{
		Summary: receipts.Summary{Receipt: receipt},
		Flags:   []sqlc.ReviewFlag{flag},
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		value any
		paths []string
	}{
		{
			name:  "receipt summary",
			value: summaryResponse(receipts.Summary{Receipt: receipt}),
			paths: []string{"createdAt"},
		},
		{
			name:  "receipt detail",
			value: detail,
			paths: []string{"createdAt", "updatedAt", "flags.0.createdAt", "flags.0.resolvedAt"},
		},
		{
			name: "product detail",
			value: productResponse(products.Detail{
				Summary: products.Summary{Product: product},
				Aliases: []products.Alias{alias},
			}),
			paths: []string{"createdAt", "updatedAt", "aliases.0.lastSeenAt"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			for _, path := range tc.paths {
				got, ok := stringAt(document, path)
				if !ok {
					t.Errorf("%s missing from response: %s", path, raw)
					continue
				}
				if !strings.HasSuffix(got, "Z") {
					t.Errorf("%s = %q, want UTC timestamp ending in Z", path, got)
				}
			}
		})
	}
}

func stringAt(document map[string]any, path string) (string, bool) {
	var current any = document
	for _, part := range strings.Split(path, ".") {
		switch node := current.(type) {
		case map[string]any:
			current = node[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				return "", false
			}
			current = node[index]
		default:
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok
}
