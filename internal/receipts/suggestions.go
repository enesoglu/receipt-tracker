package receipts

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// SuggestionStatus is what the settings page shows about the suggestion
// model: whether a suggestion endpoint is configured at all, and whether
// the newest job found it offline. The error text of a failed job never
// leaves the server.
type SuggestionStatus struct {
	// Available is false when no suggestion endpoint is configured; no
	// job was ever read in that case.
	Available bool
	// Offline is true when the newest suggestion job failed, or has not
	// succeeded and carries an error.
	Offline bool
}

// SuggestionStatus reports the state of the suggestion feature for the
// authenticated user. The per-user switch is not consulted here: the
// settings page reads it as its own field and shows "Off" before this
// badge.
func (s *Service) SuggestionStatus(ctx context.Context, userID uuid.UUID) (SuggestionStatus, error) {
	if s.enroller == nil {
		return SuggestionStatus{}, nil
	}
	job, err := sqlc.New(s.db).GetNewestSuggestLinesJob(ctx, userID.String())
	if errors.Is(err, pgx.ErrNoRows) {
		// A configured deployment whose user has not uploaded anything
		// suggestible yet.
		return SuggestionStatus{Available: true}, nil
	}
	if err != nil {
		return SuggestionStatus{}, fmt.Errorf("load newest suggestion job: %w", err)
	}
	offline := job.Status == "failed" || (job.Status != "succeeded" && job.LastError != nil)
	return SuggestionStatus{Available: true, Offline: offline}, nil
}
