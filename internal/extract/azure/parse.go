package azure

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/extract"
)

// operation is the part of an AnalyzeOperation the client reads.
type operation struct {
	Status        string        `json:"status"`
	Error         *serviceError `json:"error"`
	AnalyzeResult *struct {
		ModelID   string     `json:"modelId"`
		Documents []document `json:"documents"`
	} `json:"analyzeResult"`
}

type document struct {
	DocType string           `json:"docType"`
	Fields  map[string]field `json:"fields"`
}

// field is a DocumentField. Numbers stay json.Number so that they are read
// into decimals exactly.
type field struct {
	Type          string           `json:"type"`
	Content       *string          `json:"content"`
	Confidence    *float64         `json:"confidence"`
	ValueString   *string          `json:"valueString"`
	ValueDate     *string          `json:"valueDate"`
	ValueTime     *string          `json:"valueTime"`
	ValueNumber   *json.Number     `json:"valueNumber"`
	ValueCurrency *currencyValue   `json:"valueCurrency"`
	ValueArray    []field          `json:"valueArray"`
	ValueObject   map[string]field `json:"valueObject"`
}

type currencyValue struct {
	Amount       *json.Number `json:"amount"`
	CurrencyCode string       `json:"currencyCode"`
}

// serviceError is the error object of the service.
type serviceError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	InnerError *struct {
		Code string `json:"code"`
	} `json:"innererror"`
}

// code returns the most specific error code.
func (e *serviceError) code() string {
	if e == nil {
		return ""
	}
	if e.InnerError != nil && e.InnerError.Code != "" {
		return e.InnerError.Code
	}
	return e.Code
}

// Parse implements [extract.Extractor]: it maps a stored AnalyzeOperation.
func (c *Client) Parse(raw json.RawMessage) (extract.Result, error) {
	return parse(raw)
}

func parse(raw json.RawMessage) (extract.Result, error) {
	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		return extract.Result{}, fmt.Errorf("decode analyze result: %w", err)
	}
	if op.AnalyzeResult == nil {
		return extract.Result{}, fmt.Errorf("analyze result has no analyzeResult (status %q)", op.Status)
	}
	var fields map[string]field
	for _, doc := range op.AnalyzeResult.Documents {
		if strings.HasPrefix(doc.DocType, "receipt") {
			fields = doc.Fields
			break
		}
	}
	// A photo in which no receipt was found yields an empty result; the
	// review rules then flag the missing fields.
	result := extract.Result{Lines: []extract.Line{}}
	if fields == nil {
		return result, nil
	}
	result.StoreName = text(fields["MerchantName"], true)
	result.Date = date(fields["TransactionDate"])
	result.Time = timeOfDay(fields["TransactionTime"])
	result.Subtotal = money(fields["Subtotal"])
	result.Tax = money(fields["TotalTax"])
	result.Total = money(fields["Total"])
	result.Currency = currency(fields, "Total", "Subtotal", "TotalTax")
	for _, item := range fields["Items"].ValueArray {
		values := item.ValueObject
		if values == nil {
			continue
		}
		result.Lines = append(result.Lines, extract.Line{
			Name:        text(values["Description"], true),
			Quantity:    number(values["Quantity"]),
			Unit:        text(values["QuantityUnit"], false),
			UnitPrice:   money(values["Price"]),
			Total:       money(values["TotalPrice"]),
			ProductCode: text(values["ProductCode"], false),
			Confidence:  confidence(item),
		})
	}
	return result, nil
}

func confidence(f field) float64 {
	if f.Confidence == nil {
		return 0
	}
	return min(max(*f.Confidence, 0), 1)
}

// text reads valueString. With fromContent, the field's content stands in
// when the service gave no normalised value.
func text(f field, fromContent bool) extract.Field[string] {
	value := ""
	switch {
	case f.ValueString != nil:
		value = *f.ValueString
	case fromContent && f.Content != nil:
		value = *f.Content
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return extract.Field[string]{}
	}
	return extract.Field[string]{Value: value, Confidence: confidence(f), Found: true}
}

func date(f field) extract.Field[time.Time] {
	if f.ValueDate == nil {
		return extract.Field[time.Time]{}
	}
	parsed, err := time.Parse(time.DateOnly, *f.ValueDate)
	if err != nil {
		return extract.Field[time.Time]{}
	}
	return extract.Field[time.Time]{Value: parsed.UTC(), Confidence: confidence(f), Found: true}
}

func timeOfDay(f field) extract.Field[time.Duration] {
	if f.ValueTime == nil {
		return extract.Field[time.Duration]{}
	}
	for _, layout := range []string{time.TimeOnly, "15:04"} {
		parsed, err := time.Parse(layout, *f.ValueTime)
		if err == nil {
			since := time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute + time.Duration(parsed.Second())*time.Second
			return extract.Field[time.Duration]{Value: since, Confidence: confidence(f), Found: true}
		}
	}
	return extract.Field[time.Duration]{}
}

func decimalOf(n *json.Number) (decimal.Decimal, bool) {
	if n == nil {
		return decimal.Decimal{}, false
	}
	value, err := decimal.NewFromString(n.String())
	return value, err == nil
}

func money(f field) extract.Field[decimal.Decimal] {
	if f.ValueCurrency == nil {
		return extract.Field[decimal.Decimal]{}
	}
	value, ok := decimalOf(f.ValueCurrency.Amount)
	if !ok {
		return extract.Field[decimal.Decimal]{}
	}
	return extract.Field[decimal.Decimal]{Value: value, Confidence: confidence(f), Found: true}
}

func number(f field) extract.Field[decimal.Decimal] {
	value, ok := decimalOf(f.ValueNumber)
	if !ok {
		return extract.Field[decimal.Decimal]{}
	}
	return extract.Field[decimal.Decimal]{Value: value, Confidence: confidence(f), Found: true}
}

func isCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, c := range code {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// currency takes the ISO code of the first amount that resolved one.
func currency(fields map[string]field, names ...string) extract.Field[string] {
	for _, name := range names {
		f := fields[name]
		if f.ValueCurrency != nil && isCurrencyCode(f.ValueCurrency.CurrencyCode) {
			return extract.Field[string]{Value: f.ValueCurrency.CurrencyCode, Confidence: confidence(f), Found: true}
		}
	}
	return extract.Field[string]{}
}
