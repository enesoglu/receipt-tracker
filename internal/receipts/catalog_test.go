package receipts

import (
	"testing"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// A settings save stores the suggestion switch with the other defaults.
func TestUpdateSettings_StoresTheSuggestionSwitch(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: "sub-settings", Email: "settings@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthSettings(t.Context(), user.ID); err != nil {
		t.Fatal(err)
	}
	service := New(pool, func() time.Time { return now })

	before, err := service.Settings(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !before.LlmSuggestionsEnabled {
		t.Fatal("a new user has suggestions switched on by default")
	}
	after, err := service.UpdateSettings(t.Context(), user.ID, "EUR", "Shopping", false)
	if err != nil {
		t.Fatal(err)
	}
	if after.LlmSuggestionsEnabled {
		t.Fatal("the save did not switch suggestions off")
	}

	after, err = service.UpdateSettings(t.Context(), user.ID, "USD", "Groceries", true)
	if err != nil {
		t.Fatal(err)
	}
	if !after.LlmSuggestionsEnabled {
		t.Fatal("the save did not switch suggestions back on")
	}
}
