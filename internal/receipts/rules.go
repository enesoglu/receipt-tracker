// Package receipts maps extracted receipts, matches their products and manages review.
package receipts

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// Flag is a suspicious value found by a review rule.
type Flag struct {
	LineID uuid.NullUUID
	Kind   string
	Detail map[string]interface{}
}

// Confidence records the extractor's confidence in a receipt field.
type Confidence struct {
	Field string
	Value float64
	Found bool
}

func lowConfidence(fields []Confidence, lines []sqlc.LineItem) []Flag {
	var flags []Flag
	for _, f := range fields {
		if f.Found && f.Value < .8 {
			flags = append(flags, Flag{Kind: "low_confidence", Detail: map[string]interface{}{"field": f.Field, "confidence": f.Value}})
		}
	}
	for _, l := range lines {
		if l.Confidence != nil && *l.Confidence < .8 {
			flags = append(flags, Flag{LineID: uuid.NullUUID{UUID: l.ID, Valid: true}, Kind: "low_confidence", Detail: map[string]interface{}{"field": "line_item", "confidence": *l.Confidence}})
		}
	}
	return flags
}

func missingFields(date *time.Time, total decimal.NullDecimal) []Flag {
	var flags []Flag
	if date == nil {
		flags = append(flags, Flag{Kind: "missing_field", Detail: map[string]interface{}{"field": "purchased_on"}})
	}
	if !total.Valid {
		flags = append(flags, Flag{Kind: "missing_field", Detail: map[string]interface{}{"field": "total"}})
	}
	return flags
}

func lineMath(lines []sqlc.LineItem) []Flag {
	var flags []Flag
	for _, l := range lines {
		if !l.Quantity.Valid || !l.UnitPrice.Valid || !l.LineTotal.Valid {
			continue
		}
		expected := l.Quantity.Decimal.Mul(l.UnitPrice.Decimal)
		if expected.Sub(l.LineTotal.Decimal).Abs().GreaterThan(decimal.New(1, -2)) {
			flags = append(flags, Flag{LineID: uuid.NullUUID{UUID: l.ID, Valid: true}, Kind: "line_math", Detail: map[string]interface{}{"quantity": l.Quantity.Decimal.String(), "unitPrice": l.UnitPrice.Decimal.StringFixed(2), "lineTotal": l.LineTotal.Decimal.StringFixed(2), "expectedTotal": expected.StringFixed(2)}})
		}
	}
	return flags
}

func sumMismatch(total decimal.NullDecimal, lines []sqlc.LineItem) []Flag {
	if !total.Valid {
		return nil
	}
	sum := decimal.Zero
	for _, l := range lines {
		if l.LineTotal.Valid {
			sum = sum.Add(l.LineTotal.Decimal)
		}
	}
	diff := total.Decimal.Sub(sum)
	if diff.Abs().GreaterThan(decimal.New(2, -2)) {
		return []Flag{{Kind: "sum_mismatch", Detail: map[string]interface{}{"lineSum": sum.StringFixed(2), "total": total.Decimal.StringFixed(2), "difference": diff.StringFixed(2)}}}
	}
	return nil
}

func possibleDuplicate(ids []uuid.UUID) []Flag {
	if len(ids) == 0 {
		return nil
	}
	return []Flag{{Kind: "possible_duplicate", Detail: map[string]interface{}{"otherReceiptId": ids[0]}}}
}

func uncertainMatch(lineID uuid.UUID, d matching.Decision, candidates []map[string]interface{}) []Flag {
	if d.Outcome != matching.Ask || d.Reason == matching.ReasonCodeConflict {
		return nil
	}
	return []Flag{{LineID: uuid.NullUUID{UUID: lineID, Valid: true}, Kind: "uncertain_match", Detail: map[string]interface{}{"reason": string(d.Reason), "candidates": candidates}}}
}

func codeConflict(lineID uuid.UUID, code *string, d matching.Decision, candidates []map[string]interface{}) []Flag {
	if d.Outcome != matching.Ask || d.Reason != matching.ReasonCodeConflict {
		return nil
	}
	var owner, other map[string]interface{}
	for i, c := range d.Candidates {
		if c.OwnsCode {
			owner = candidates[i]
		} else if other == nil {
			other = candidates[i]
		}
	}
	return []Flag{{LineID: uuid.NullUUID{UUID: lineID, Valid: true}, Kind: "code_conflict", Detail: map[string]interface{}{"code": code, "codeProduct": owner, "nameProduct": other}}}
}
