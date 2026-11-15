package receipts

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

func number(s string) decimal.NullDecimal {
	return decimal.NewNullDecimal(decimal.RequireFromString(s))
}

func TestReviewRules_Boundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() []Flag
		want int
	}{
		{"confidence below", func() []Flag { return lowConfidence([]Confidence{{"total", .799, true}}, nil) }, 1},
		{"confidence at", func() []Flag { return lowConfidence([]Confidence{{"total", .8, true}}, nil) }, 0},
		{"confidence above", func() []Flag { return lowConfidence([]Confidence{{"total", .801, true}}, nil) }, 0},
		{"absent confidence", func() []Flag { return lowConfidence([]Confidence{{"total", 0, false}}, nil) }, 0},
		{"missing both", func() []Flag { return missingFields(nil, decimal.NullDecimal{}) }, 2},
		{"missing date", func() []Flag { return missingFields(nil, number("0")) }, 1},
		{"missing total", func() []Flag { return missingFields(new(time.Time), decimal.NullDecimal{}) }, 1},
		{"present zero", func() []Flag { return missingFields(new(time.Time), number("0")) }, 0},
		{"duplicate", func() []Flag { return possibleDuplicate([]uuid.UUID{uuid.New()}) }, 1},
		{"no duplicate", func() []Flag { return possibleDuplicate(nil) }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(tc.run()); got != tc.want {
				t.Fatalf("flags=%d want %d", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		value string
		want  int
	}{{"9.98", 1}, {"9.99", 0}, {"10", 0}, {"10.01", 0}, {"10.02", 1}} {
		t.Run("line math "+tc.value, func(t *testing.T) {
			got := lineMath([]sqlc.LineItem{{Quantity: number("0.5"), UnitPrice: number("20"), LineTotal: number(tc.value)}})
			if len(got) != tc.want {
				t.Fatalf("flags=%v want %d", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		value string
		want  int
	}{{"7.97", 1}, {"7.98", 0}, {"8", 0}, {"8.02", 0}, {"8.03", 1}} {
		t.Run("sum "+tc.value, func(t *testing.T) {
			got := sumMismatch(number(tc.value), []sqlc.LineItem{{LineTotal: number("10")}, {LineTotal: number("-2")}})
			if len(got) != tc.want {
				t.Fatalf("flags=%v want %d", got, tc.want)
			}
		})
	}
	if got := lineMath([]sqlc.LineItem{{Quantity: number("1"), UnitPrice: number("-2"), LineTotal: number("-2")}}); len(got) != 0 {
		t.Fatalf("valid discount flags=%v", got)
	}
	if got := lineMath([]sqlc.LineItem{{LineTotal: number("2")}}); len(got) != 0 {
		t.Fatalf("missing operands flags=%v", got)
	}
	f := sumMismatch(number("11"), []sqlc.LineItem{{LineTotal: number("10")}})
	if diff := cmp.Diff(map[string]interface{}{"lineSum": "10.00", "total": "11.00", "difference": "1.00"}, f[0].Detail); diff != "" {
		t.Fatal(diff)
	}
}

func TestReviewRules_MatchReasons(t *testing.T) {
	for _, reason := range []matching.Reason{matching.ReasonAmbiguousCode, matching.ReasonCodeNameMismatch, matching.ReasonSizeConflict, matching.ReasonSimilarName, matching.ReasonCodeConflict} {
		t.Run(string(reason), func(t *testing.T) {
			lineID := uuid.New()
			code := "1234567"
			d := matching.Decision{Outcome: matching.Ask, Reason: reason, Candidates: []matching.CandidateScore{{OwnsCode: true}, {}}}
			candidates := []map[string]interface{}{{"canonicalName": "OWNER"}, {"canonicalName": "NAME"}}
			flags := uncertainMatch(lineID, d, candidates)
			flags = append(flags, codeConflict(lineID, &code, d, candidates)...)
			kind := "uncertain_match"
			if reason == matching.ReasonCodeConflict {
				kind = "code_conflict"
			}
			if len(flags) != 1 || flags[0].Kind != kind || flags[0].LineID.UUID != lineID {
				t.Fatalf("flags=%+v", flags)
			}
			if kind == "uncertain_match" && flags[0].Detail["reason"] != string(reason) {
				t.Fatalf("detail=%v", flags[0].Detail)
			}
		})
	}
	d := matching.Decision{Outcome: matching.AutoLink, Reason: matching.ReasonCodeMatch}
	if len(uncertainMatch(uuid.New(), d, nil)) != 0 || len(codeConflict(uuid.New(), nil, d, nil)) != 0 {
		t.Fatal("linked line was flagged")
	}
}
