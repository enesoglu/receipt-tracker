package suggestions

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// SettingsEnroller is the receipts.Enroller of a configured deployment:
// it reads the per-user switch from the user's settings row. The
// receipts package owns the interface; this type satisfies it without
// importing it.
type SettingsEnroller struct {
	db     *sqlc.Queries
	logger *slog.Logger
}

// NewSettingsEnroller returns the enroller reading one database.
func NewSettingsEnroller(db sqlc.DBTX, logger *slog.Logger) *SettingsEnroller {
	return &SettingsEnroller{db: sqlc.New(db), logger: logger}
}

// LLMSuggestionsEnabled reports whether the user has line suggestions
// switched on. A read failure is logged and counts as switched off: it
// must never fail the extraction that asks.
func (e *SettingsEnroller) LLMSuggestionsEnabled(ctx context.Context, userID uuid.UUID) bool {
	settings, err := e.db.GetOwnedSettings(ctx, userID)
	if err != nil {
		e.logger.Error("read suggestion settings", "user_id", userID, "error", err)
		return false
	}
	return settings.LlmSuggestionsEnabled
}
