package apperr_test

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

func allKinds() []apperr.Kind {
	return []apperr.Kind{apperr.NotFound, apperr.Invalid, apperr.Conflict, apperr.Forbidden, apperr.Unavailable}
}

func TestKind_MatchesOnlyItself(t *testing.T) {
	for _, kind := range allKinds() {
		t.Run(kind.Error(), func(t *testing.T) {
			candidates := map[string]error{
				"bare kind":              kind,
				"wrapped bare kind":      fmt.Errorf("load receipt: %w", kind),
				"classified":             apperr.New(kind, "message for the user"),
				"classified and wrapped": fmt.Errorf("load receipt: %w", apperr.Wrap(kind, "message for the user", io.ErrUnexpectedEOF)),
			}
			for name, err := range candidates {
				for _, other := range allKinds() {
					if got, want := errors.Is(err, other), other == kind; got != want {
						t.Errorf("%s: errors.Is(err, %v) = %v, want %v", name, other, got, want)
					}
				}
				got, ok := apperr.KindOf(err)
				if !ok || got != kind {
					t.Errorf("%s: KindOf = %v, %v, want %v, true", name, got, ok, kind)
				}
			}
		})
	}
}

func TestKindOf_ReportsUnclassifiedErrors(t *testing.T) {
	for name, err := range map[string]error{
		"nil":           nil,
		"plain":         io.ErrUnexpectedEOF,
		"wrapped plain": fmt.Errorf("read image: %w", io.ErrUnexpectedEOF),
	} {
		if kind, ok := apperr.KindOf(err); ok {
			t.Errorf("%s: KindOf = %v, true, want no kind", name, kind)
		}
	}
}

func TestKindOf_PrefersTheOutermostClassification(t *testing.T) {
	inner := apperr.New(apperr.NotFound, "drive file is gone")
	outer := apperr.Wrap(apperr.Unavailable, "image storage is not reachable", inner)
	got, ok := apperr.KindOf(fmt.Errorf("open image: %w", outer))
	if !ok || got != apperr.Unavailable {
		t.Fatalf("KindOf = %v, %v, want %v, true", got, ok, apperr.Unavailable)
	}
}

func TestError_KeepsTheCauseOutOfTheMessage(t *testing.T) {
	cause := errors.New("dial tcp 10.0.0.5:5432: connection refused")
	err := apperr.Wrap(apperr.Unavailable, "the database is not reachable", cause)
	if err.Message != "the database is not reachable" {
		t.Errorf("Message = %q, want only the user message", err.Message)
	}
	if got, want := err.Error(), "the database is not reachable: dial tcp 10.0.0.5:5432: connection refused"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is does not reach the cause")
	}
	var classified *apperr.Error
	if !errors.As(fmt.Errorf("ping: %w", err), &classified) || classified != err {
		t.Error("errors.As does not find the classified error")
	}
}

func TestError_FallsBackToTheKindWithoutAMessage(t *testing.T) {
	if got, want := apperr.New(apperr.Conflict, "").Error(), "conflict"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestError_WithCodeAndWithFieldReturnCopies(t *testing.T) {
	base := apperr.New(apperr.Invalid, "the line item is not valid")
	first := base.WithField("lineTotal", "must be a decimal amount")
	second := first.WithField("name", "must not be empty").WithCode("line_item_invalid")

	if base.Code != "" || len(base.Fields) != 0 {
		t.Errorf("base was modified: %+v", base)
	}
	if diff := cmp.Diff([]apperr.FieldError{{Field: "lineTotal", Message: "must be a decimal amount"}}, first.Fields); diff != "" {
		t.Errorf("first fields mismatch (-want +got):\n%s", diff)
	}
	want := []apperr.FieldError{
		{Field: "lineTotal", Message: "must be a decimal amount"},
		{Field: "name", Message: "must not be empty"},
	}
	if diff := cmp.Diff(want, second.Fields); diff != "" {
		t.Errorf("second fields mismatch (-want +got):\n%s", diff)
	}
	if second.Code != "line_item_invalid" || !errors.Is(second, apperr.Invalid) {
		t.Errorf("second lost its code or kind: %+v", second)
	}
}

func TestKind_DescribesItself(t *testing.T) {
	want := map[apperr.Kind]string{
		apperr.NotFound:    "not found",
		apperr.Invalid:     "invalid",
		apperr.Conflict:    "conflict",
		apperr.Forbidden:   "forbidden",
		apperr.Unavailable: "unavailable",
		apperr.Kind(0):     "unknown error kind",
	}
	for kind, text := range want {
		if got := kind.Error(); got != text {
			t.Errorf("Kind(%d).Error() = %q, want %q", kind, got, text)
		}
	}
}
