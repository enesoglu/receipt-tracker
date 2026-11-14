package fake_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/extract/fake"
)

type meter struct {
	pages int
	err   error
}

func (m *meter) Count(_ context.Context, pages int) error {
	if m.err != nil {
		return m.err
	}
	m.pages += pages
	return nil
}

func TestExtract_SameImageSameResultAndParsesBack(t *testing.T) {
	m := &meter{}
	extractor := fake.New(m)
	img := extract.Image{Data: []byte("synthetic jpeg bytes"), ContentType: "image/jpeg"}

	first, err := extractor.Extract(t.Context(), img)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	second, err := extractor.Extract(t.Context(), img)
	if err != nil {
		t.Fatalf("extract again: %v", err)
	}
	if diff := cmp.Diff(first.Result, second.Result); diff != "" {
		t.Errorf("results differ for the same image (-first +second):\n%s", diff)
	}
	if !first.Result.Total.Found || first.Model != "fake-receipt" || extractor.Provider() != extract.ProviderFake {
		t.Errorf("extraction = %+v", first)
	}
	parsed, err := extractor.Parse(first.Raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if diff := cmp.Diff(first.Result, parsed); diff != "" {
		t.Errorf("parsed result mismatch (-extracted +parsed):\n%s", diff)
	}
	if m.pages != 2 {
		t.Errorf("counted %d pages, want 2", m.pages)
	}
}

func TestExtract_DifferentImagesCoverEverySample(t *testing.T) {
	extractor := fake.New(&meter{})
	totals := map[string]bool{}
	for i := range 64 {
		got, err := extractor.Extract(t.Context(), extract.Image{Data: []byte{byte(i), 1, 2, 3}, ContentType: "image/png"})
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		totals[got.Result.Total.Value.String()] = true
	}
	if len(totals) != 3 {
		t.Errorf("saw %d distinct samples, want all 3", len(totals))
	}
}

func TestExtract_RefusesOtherImageTypes(t *testing.T) {
	_, err := fake.New(&meter{}).Extract(t.Context(), extract.Image{Data: []byte("x"), ContentType: "image/heic"})
	var extractErr *extract.Error
	if !errors.As(err, &extractErr) || !extractErr.Permanent {
		t.Fatalf("error = %v, want a permanent extraction error", err)
	}
}

func TestExtract_MeterFailureIsRetryable(t *testing.T) {
	_, err := fake.New(&meter{err: errors.New("database unavailable")}).Extract(t.Context(), extract.Image{Data: []byte("x"), ContentType: "image/jpeg"})
	var extractErr *extract.Error
	if !errors.As(err, &extractErr) || extractErr.Permanent {
		t.Fatalf("error = %v, want a retryable extraction error", err)
	}
}

func TestParse_RefusesGarbage(t *testing.T) {
	if _, err := fake.New(&meter{}).Parse([]byte("not json")); err == nil {
		t.Fatal("parse of garbage succeeded")
	}
}
