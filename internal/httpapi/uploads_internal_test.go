package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// deadlineWriter records the deadlines http.ResponseController sets.
type deadlineWriter struct {
	*httptest.ResponseRecorder
	read, write time.Time
}

func (w *deadlineWriter) SetReadDeadline(t time.Time) error  { w.read = t; return nil }
func (w *deadlineWriter) SetWriteDeadline(t time.Time) error { w.write = t; return nil }

func TestUploadDeadlines_ExtendOnlyTheUploadOperation(t *testing.T) {
	next := func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) { return nil, nil }
	for _, tt := range []struct {
		operation string
		extended  bool
	}{
		{"UploadReceipts", true},
		{"ListReceipts", false},
	} {
		w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/receipts", nil)
		before := time.Now()
		if _, err := uploadDeadlines(next, tt.operation)(t.Context(), w, r, nil); err != nil {
			t.Fatalf("%s: %v", tt.operation, err)
		}
		extended := !w.read.Before(before.Add(uploadTimeout)) && !w.write.Before(before.Add(uploadTimeout))
		if extended != tt.extended {
			t.Errorf("%s: read deadline %v, write deadline %v; extended = %v, want %v", tt.operation, w.read, w.write, extended, tt.extended)
		}
	}
}
