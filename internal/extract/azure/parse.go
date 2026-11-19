package azure

import (
	"encoding/json"
	"fmt"
	"regexp"
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
		Content   string     `json:"content"`
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
	// review rules then flag the missing fields. The e-archive identity is
	// read from the printed text whenever the response carries it, with or
	// without a recognised receipt document.
	result := extract.Result{Lines: []extract.Line{}}
	if op.AnalyzeResult != nil {
		result.InvoiceNumber, result.Ettn, result.SellerTaxNumber = readIdentity(op.AnalyzeResult.Content)
	}
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
			Unit:        unit(values),
			UnitPrice:   money(values["Price"]),
			Total:       lineTotal(item, values["TotalPrice"]),
			ProductCode: text(values["ProductCode"], false),
			Confidence:  confidence(item),
		})
	}
	return extract.CompleteTotal(result), nil
}

// unit reads an item's QuantityUnit. When the service found a quantity but
// no unit, the unit printed in the unit price ("114,90 TL/kg") stands in;
// it belongs to the same printed block. A package size in the name
// ("680 GR") is never used: it is not the unit of the purchase.
func unit(values map[string]field) extract.Field[string] {
	found := text(values["QuantityUnit"], false)
	if found.Found || !number(values["Quantity"]).Found {
		return found
	}
	price := values["Price"]
	if price.Content == nil {
		return found
	}
	priceDenominator := regexp.MustCompile(`(?i)/\s*(kg|kgr|g|gr|lt|l|adet|ad)\s*$`)
	match := priceDenominator.FindStringSubmatch(*price.Content)
	if match == nil {
		return found
	}
	var value string
	switch strings.ToLower(match[1]) {
	case "kg", "kgr":
		value = "KG"
	case "g", "gr":
		value = "G"
	case "l", "lt":
		value = "L"
	case "ad", "adet":
		value = "AD"
	}
	return extract.Field[string]{Value: value, Confidence: confidence(price), Found: true}
}

// lineTotal reads an item's TotalPrice and keeps the minus sign that the
// receipt prints in front of a discount. The service has been seen to
// return a positive amount for a discount printed as "*- 54.90": the minus
// is in the item's content, but neither in the amount nor in the
// TotalPrice field's own content. A minus after an amount ("79,90-B", a
// VAT class) does not count.
func lineTotal(item, totalPrice field) extract.Field[decimal.Decimal] {
	total := money(totalPrice)
	if total.Found && total.Value.IsPositive() && printedNegative(item, totalPrice) {
		total.Value = total.Value.Neg()
	}
	return total
}

// printedNegative reports whether a minus sign stands right before the
// printed line total, with only spaces and the "*" amount marker between.
func printedNegative(item, totalPrice field) bool {
	if totalPrice.Content == nil {
		return false
	}
	printed := strings.TrimSpace(*totalPrice.Content)
	if printed == "" {
		return false
	}
	if strings.HasPrefix(strings.TrimLeft(printed, " *"), "-") {
		return true
	}
	if item.Content == nil {
		return false
	}
	at := strings.LastIndex(*item.Content, printed)
	if at < 0 {
		return false
	}
	return strings.HasSuffix(strings.TrimRight((*item.Content)[:at], " *"), "-")
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
