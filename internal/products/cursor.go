package products

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// pageCursor is where the next page of the product list starts: after the
// product with this name and id. Scope binds it to the user and filters it
// was issued for, so it cannot be replayed with others.
type pageCursor struct {
	Version int       `json:"v"`
	Scope   string    `json:"s"`
	Name    string    `json:"n"`
	ID      uuid.UUID `json:"i"`
}

const (
	defaultLimit = 50
	maxLimit     = 100
	maxCursor    = 512
)

func pageLimit(limit int) (int32, error) {
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit < 1 || limit > maxLimit {
		return 0, apperr.New(apperr.Invalid, "limit must be between 1 and 100").WithField("limit", "must be between 1 and 100")
	}
	return int32(limit), nil
}

func listScope(userID uuid.UUID, f Filter, tokens []string) string {
	raw, _ := json.Marshal(struct {
		Kind     string
		UserID   uuid.UUID
		Category uuid.NullUUID
		Tokens   []string
	}{"products", userID, f.CategoryID, tokens})
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func encodeCursor(c pageCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(value, scope string) (pageCursor, error) {
	invalid := apperr.New(apperr.Invalid, "invalid cursor for these filters").WithField("cursor", "is not a cursor of this list")
	if len(value) > maxCursor {
		return pageCursor{}, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return pageCursor{}, invalid
	}
	var c pageCursor
	if json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Scope != scope || c.ID == uuid.Nil {
		return pageCursor{}, invalid
	}
	return c, nil
}
