// Package extract reads receipt images into structured data through a
// provider, behind the [Extractor] interface: Azure Document Intelligence in
// production (package azure) and synthetic results for development and tests
// (package fake).
//
// The [Job] runs one extraction per receipt from the job queue. It checks
// the monthly page allowance first ([Usage]), sets the receipt to
// processing, calls the extractor, saves the provider's raw result in
// extractions and hands the provider-neutral [Result] to a [ResultHandler].
// Because the raw result is kept, a retry or a later reprocess maps it again
// with [Extractor.Parse] instead of spending another page of the allowance.
package extract

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

// The providers, as recorded in extractions.provider and provider_usage.
const (
	ProviderAzure = "azure"
	ProviderFake  = "fake"
)

// Image is a receipt image as stored: JPEG or PNG. Ingest converts every
// other format before an image is stored.
type Image struct {
	Data        []byte
	ContentType string
}

// Field is one extracted value. Found is false when the provider did not
// report the field or reported it in a form that cannot be read; Value is
// then the zero value. Confidence is the provider's confidence from 0 to 1.
type Field[T any] struct {
	Value      T       `json:"value"`
	Confidence float64 `json:"confidence"`
	Found      bool    `json:"found"`
}

// Result is what a provider read from one receipt, in a form that does not
// depend on the provider.
type Result struct {
	StoreName Field[string] `json:"storeName"`
	// Date is the purchase date at midnight UTC.
	Date Field[time.Time] `json:"date"`
	// Time is the purchase time of day, as the time since midnight.
	Time Field[time.Duration] `json:"time"`
	// Currency is an ISO 4217 code such as "TRY".
	Currency Field[string]          `json:"currency"`
	Subtotal Field[decimal.Decimal] `json:"subtotal"`
	Tax      Field[decimal.Decimal] `json:"tax"`
	Total    Field[decimal.Decimal] `json:"total"`
	Lines    []Line                 `json:"lines"`
}

// Line is one extracted line item. A negative Total is a discount.
type Line struct {
	Name        Field[string]          `json:"name"`
	Quantity    Field[decimal.Decimal] `json:"quantity"`
	Unit        Field[string]          `json:"unit"`
	UnitPrice   Field[decimal.Decimal] `json:"unitPrice"`
	Total       Field[decimal.Decimal] `json:"total"`
	ProductCode Field[string]          `json:"productCode"`
	// Confidence is the provider's confidence in the line as a whole.
	Confidence float64 `json:"confidence"`
}

// Extraction is one completed extraction.
type Extraction struct {
	// Model names what the provider ran, such as "prebuilt-receipt".
	Model string
	// Raw is the provider's response, kept so that it can be mapped again.
	Raw    json.RawMessage
	Result Result
}

// Extractor reads receipt images through one provider.
type Extractor interface {
	// Provider returns the provider name: ProviderAzure or ProviderFake.
	Provider() string
	// Extract reads img. A failure is reported as an [*Error].
	Extract(ctx context.Context, img Image) (Extraction, error)
	// Parse maps a raw result that Extract returned earlier.
	Parse(raw json.RawMessage) (Result, error)
}

// Error is a failed extraction.
type Error struct {
	// Permanent is true when trying again cannot help: the provider
	// refused the image, or the image is unusable.
	Permanent bool
	// Code is the provider's error code when it gave one, such as
	// "InvalidContent".
	Code string
	// Reason says what went wrong in words that are safe to show the user.
	Reason string
	cause  error
}

// NewError returns an extraction error with a reason for the user and the
// cause for logs. The cause must not contain secrets.
func NewError(permanent bool, code, reason string, cause error) *Error {
	return &Error{Permanent: permanent, Code: code, Reason: reason, cause: cause}
}

// Error returns the reason, the code and the cause, for logs.
func (e *Error) Error() string {
	message := "extract receipt: " + e.Reason
	if e.Code != "" {
		message += " (" + e.Code + ")"
	}
	if e.cause != nil {
		message += ": " + e.cause.Error()
	}
	return message
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.cause }
