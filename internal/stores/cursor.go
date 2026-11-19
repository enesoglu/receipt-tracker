package stores

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// pageCursor is where the next page of a list starts: after the row with
// this id and name. Scope binds it to the user and filters it was issued
// for, so it cannot be replayed with others.
type pageCursor struct {
	Version int       `json:"v"`
	Scope   string    `json:"s"`
	ID      uuid.UUID `json:"i"`
	Created time.Time `json:"t"`
	Name    string    `json:"n,omitempty"`
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

// cursorScope binds a cursor to one list of one user: the hash covers the
// kind, the user and the filters, so a cursor of one list cannot be
// replayed on another.
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
