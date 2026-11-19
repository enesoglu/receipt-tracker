package receipts

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

func flagKey(kind string, lineID uuid.NullUUID, detail []byte) string {
	var data map[string]interface{}
	_ = json.Unmarshal(detail, &data)
	canonical, _ := json.Marshal(data)
	return kind + ":" + lineID.UUID.String() + ":" + string(canonical)
}

func (s *Service) syncFlags(ctx context.Context, q *unit, userID, receiptID uuid.UUID, flags []Flag, previous []sqlc.ReviewFlag, restore bool) error {
	existing := make(map[string]sqlc.ReviewFlag, len(previous))
	for _, f := range previous {
		if !keepsCause(f) {
			continue
		}
		existing[flagKey(f.Kind, f.LineItemID, f.Detail)] = f
	}
	wanted := make(map[string]bool, len(flags))
	for _, f := range flags {
		detail, err := json.Marshal(f.Detail)
		if err != nil {
			return err
		}
		key := flagKey(f.Kind, f.LineID, detail)
		if wanted[key] {
			continue
		}
		wanted[key] = true
		old, found := existing[key]
		if found {
			if restore {
				if err := q.RestoreReviewFlag(ctx, sqlc.RestoreReviewFlagParams{ID: old.ID, UserID: userID, ReceiptID: receiptID, LineItemID: f.LineID, Kind: f.Kind, Detail: detail, CreatedAt: old.CreatedAt, ResolvedAt: old.ResolvedAt, Resolution: old.Resolution}); err != nil {
					return err
				}
			}
			continue
		}
		if err := q.InsertReviewFlag(ctx, sqlc.InsertReviewFlagParams{UserID: userID, ReceiptID: receiptID, LineItemID: f.LineID, Kind: f.Kind, Detail: detail, Now: s.now().UTC()}); err != nil {
			return err
		}
	}
	if !restore {
		for key, old := range existing {
			if wanted[key] {
				continue
			}
			if old.ResolvedAt == nil {
				if err := s.closeFlag(ctx, q, userID, old.ID, "superseded"); err != nil {
					return err
				}
				continue
			}
			// An accepted or confirmed flag is no longer a live cause once
			// the data changes. Mark it superseded so the same cause can
			// raise a fresh flag if it comes back.
			if err := q.SupersedeAcceptedReviewFlag(ctx, sqlc.SupersedeAcceptedReviewFlagParams{UserID: userID, FlagID: old.ID}); err != nil {
				return err
			}
		}
	}
	return q.RefreshReviewStatus(ctx, sqlc.RefreshReviewStatusParams{UserID: userID, ReceiptID: receiptID, Now: s.now().UTC()})
}

func (s *Service) refresh(ctx context.Context, q *unit, userID, receiptID uuid.UUID, dropFields map[string]bool, rematched map[uuid.UUID]bool, extra []Flag) error {
	previous, err := q.GetReceiptFlags(ctx, sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return err
	}
	fields := []Confidence{}
	for _, f := range previous {
		var detail map[string]interface{}
		if err := json.Unmarshal(f.Detail, &detail); err != nil {
			return err
		}
		switch f.Kind {
		case "low_confidence":
			if !f.LineItemID.Valid && keepsCause(f) {
				field, _ := detail["field"].(string)
				confidence, _ := detail["confidence"].(float64)
				if !dropFields[field] {
					fields = append(fields, Confidence{Field: field, Value: confidence, Found: true})
				}
			}
		case "uncertain_match", "code_conflict":
			if f.ResolvedAt == nil && !rematched[f.LineItemID.UUID] {
				extra = append(extra, Flag{LineID: f.LineItemID, Kind: f.Kind, Detail: detail})
			}
		case "new_store":
			// A store edit or correction answers the question the flag
			// asked, so it is dropped; anything else leaves it open.
			if f.ResolvedAt == nil && !dropFields["store"] {
				extra = append(extra, Flag{LineID: f.LineItemID, Kind: f.Kind, Detail: detail})
			}
		}
	}
	flags, err := s.evaluate(ctx, q, userID, receiptID, fields, extra)
	if err != nil {
		return err
	}
	return s.syncFlags(ctx, q, userID, receiptID, flags, previous, false)
}

func keepsCause(f sqlc.ReviewFlag) bool {
	return f.Resolution == nil || *f.Resolution == "accepted" || *f.Resolution == "confirmed"
}

func (s *Service) closeFlag(ctx context.Context, q *unit, userID, flagID uuid.UUID, resolution string) error {
	now := s.now().UTC()
	return q.CloseReviewFlag(ctx, sqlc.CloseReviewFlagParams{UserID: userID, FlagID: flagID, Resolution: &resolution, Now: &now})
}
