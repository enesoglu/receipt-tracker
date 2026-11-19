package receipts

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

type pageCursor struct {
	Version   int        `json:"v"`
	Scope     string     `json:"s"`
	ID        uuid.UUID  `json:"i"`
	Date      *time.Time `json:"d,omitempty"`
	Created   time.Time  `json:"t"`
	Secondary *time.Time `json:"u,omitempty"`
	Name      string     `json:"n,omitempty"`
}

func pageLimit(limit int) (int32, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, apperr.New(apperr.Invalid, "limit must be between 1 and 100")
	}
	return int32(limit), nil
}

func receiptScope(userID uuid.UUID, f Filter) string {
	ids := slices.Clone(f.IDs)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return cursorScope("receipts", userID, struct {
		IDs           []uuid.UUID
		Status, Month string
		Store         uuid.NullUUID
		Category      uuid.NullUUID
		Group, Search string
	}{ids, f.Status, f.Month, f.StoreID, f.CategoryID, f.Group, f.Search})
}

func cursorScope(kind string, userID uuid.UUID, filters interface{}) string {
	raw, _ := json.Marshal(struct {
		Kind    string
		UserID  uuid.UUID
		Filters interface{}
	}{kind, userID, filters})
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func encodeCursor(c pageCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(value, scope string) (pageCursor, error) {
	var c pageCursor
	if len(value) > 512 {
		return c, apperr.New(apperr.Invalid, "invalid cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Scope != scope || c.ID == uuid.Nil || c.Created.IsZero() || strings.ContainsRune(c.Name, 0) {
		return pageCursor{}, apperr.New(apperr.Invalid, "invalid cursor for these filters")
	}
	return c, nil
}
