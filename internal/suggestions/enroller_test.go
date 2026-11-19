package suggestions_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/suggestions"
)

// The enroller satisfies the seam the receipts package consumes.
var _ receipts.Enroller = (*suggestions.SettingsEnroller)(nil)

func newEnrollerFixture(t *testing.T) (*sqlc.Queries, uuid.UUID, *suggestions.SettingsEnroller) {
	t.Helper()
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "synthetic@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return q, user.ID, suggestions.NewSettingsEnroller(pool, logger)
}

func TestSettingsEnroller_DefaultIsSwitchedOn(t *testing.T) {
	q, user, enroller := newEnrollerFixture(t)
	if err := q.CreateAuthSettings(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	if !enroller.LLMSuggestionsEnabled(t.Context(), user) {
		t.Error("a new user has suggestions switched on by default")
	}
}

func TestSettingsEnroller_SwitchedOff(t *testing.T) {
	q, user, enroller := newEnrollerFixture(t)
	if err := q.CreateAuthSettings(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateOwnedSettings(t.Context(), sqlc.UpdateOwnedSettingsParams{
		UserID: user, DefaultCurrency: "TRY", DefaultReceiptTitle: "Receipt",
		LlmSuggestionsEnabled: false, Now: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if enroller.LLMSuggestionsEnabled(t.Context(), user) {
		t.Error("the switch is off, want switched off")
	}
}

func TestSettingsEnroller_ReadFailureCountsAsOff(t *testing.T) {
	_, user, enroller := newEnrollerFixture(t)
	// No settings row exists: the read fails, and the extraction that
	// asked must not fail with it.
	if enroller.LLMSuggestionsEnabled(t.Context(), user) {
		t.Error("a failed settings read must count as switched off")
	}
}
