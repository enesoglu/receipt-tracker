// Package fake is an extractor that returns synthetic results, for
// development without an Azure resource and for tests (EXTRACTOR=fake). The
// same image always yields the same result: the sample is chosen by the
// image's SHA-256.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/extract"
)

// Meter counts the pages an extractor submits.
type Meter interface {
	Count(ctx context.Context, pages int) error
}

// Extractor is the fake provider.
type Extractor struct {
	meter Meter
}

// New returns a fake extractor that counts one page per image on meter, so
// that the monthly allowance can be tried out without Azure.
func New(meter Meter) *Extractor {
	return &Extractor{meter: meter}
}

// model is what the fake records as the model that read the image.
const model = "fake-receipt"

// raw is the fake's stored response.
type raw struct {
	Sample int            `json:"sample"`
	Result extract.Result `json:"result"`
}

// Provider implements [extract.Extractor].
func (e *Extractor) Provider() string { return extract.ProviderFake }

// Extract implements [extract.Extractor].
func (e *Extractor) Extract(ctx context.Context, img extract.Image) (extract.Extraction, error) {
	if img.ContentType != "image/jpeg" && img.ContentType != "image/png" {
		return extract.Extraction{}, extract.NewError(true, "", "Only JPEG and PNG images can be read.", fmt.Errorf("content type %q", img.ContentType))
	}
	if err := e.meter.Count(ctx, 1); err != nil {
		return extract.Extraction{}, extract.NewError(false, "", "The page counter could not be updated.", err)
	}
	digest := sha256.Sum256(img.Data)
	index := int(digest[0]) % len(samples())
	document, err := json.Marshal(raw{Sample: index, Result: samples()[index]})
	if err != nil {
		return extract.Extraction{}, extract.NewError(false, "", "The synthetic result could not be encoded.", err)
	}
	return extract.Extraction{Model: model, Raw: document, Result: samples()[index]}, nil
}

// Parse implements [extract.Extractor].
func (e *Extractor) Parse(document json.RawMessage) (extract.Result, error) {
	var stored raw
	if err := json.Unmarshal(document, &stored); err != nil {
		return extract.Result{}, fmt.Errorf("decode fake result: %w", err)
	}
	return stored.Result, nil
}

func text(value string, confidence float64) extract.Field[string] {
	return extract.Field[string]{Value: value, Confidence: confidence, Found: true}
}

func amount(value string, confidence float64) extract.Field[decimal.Decimal] {
	return extract.Field[decimal.Decimal]{Value: decimal.RequireFromString(value), Confidence: confidence, Found: true}
}

func line(name, quantity, unitPrice, total string, confidence float64) extract.Line {
	l := extract.Line{Name: text(name, confidence), Total: amount(total, confidence), Confidence: confidence}
	if quantity != "" {
		l.Quantity = amount(quantity, confidence)
	}
	if unitPrice != "" {
		l.UnitPrice = amount(unitPrice, confidence)
	}
	return l
}

// samples are synthetic receipts. They exercise what the review rules look
// at: a clean receipt, one with a discount line and a low-confidence total,
// and one with no date.
func samples() []extract.Result {
	day := func(d int) extract.Field[time.Time] {
		return extract.Field[time.Time]{Value: time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC), Confidence: 0.98, Found: true}
	}
	at := func(h, m int) extract.Field[time.Duration] {
		return extract.Field[time.Duration]{Value: time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, Confidence: 0.95, Found: true}
	}
	return []extract.Result{
		{
			StoreName: text("SYNTHETIC MARKET", 0.97), Date: day(3), Time: at(18, 42), Currency: text("TRY", 0.9),
			Subtotal: amount("112.50", 0.96), Tax: amount("8.33", 0.93), Total: amount("120.83", 0.98),
			Lines: []extract.Line{
				line("SUT 1LT", "2", "32.50", "65.00", 0.95),
				line("EKMEK", "1", "15.00", "15.00", 0.97),
				line("DOMATES KG", "1.25", "26.00", "32.50", 0.91),
			},
		},
		{
			StoreName: text("EXAMPLE GROCER", 0.92), Date: day(9), Time: at(10, 5), Currency: text("TRY", 0.9),
			Total: amount("84.50", 0.62),
			Lines: []extract.Line{
				line("PEYNIR 500G", "1", "99.50", "99.50", 0.88),
				line("INDIRIM", "", "", "-15.00", 0.84),
			},
		},
		{
			StoreName: text("TEST BAKKAL", 0.71), Currency: text("TRY", 0.9), Total: amount("27.00", 0.95),
			Lines: []extract.Line{line("AYRAN", "3", "9.00", "27.00", 0.93)},
		},
	}
}
