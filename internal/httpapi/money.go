package httpapi

import (
	"regexp"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// The shapes one decimal input may take, after the comma has become a dot:
// digits with an optional fraction ("351", "351.9", "5."), or a fraction
// alone (".5"). A separator is accepted with digits on at least one side;
// a value holding both separators is rejected before the shape is tried,
// because a thousands group such as "1.234,56" cannot be read without a
// locale. The shapes are sign-agnostic on purpose: the sign rule is
// semantic and lives in the services, which reject anything but positive
// quantities while money may be a discount.
const (
	moneyPattern    = `^-?(\d{1,10}([.,]\d{0,2})?|[.,]\d{1,2})$`
	quantityPattern = `^-?(\d{1,9}([.,]\d{0,3})?|[.,]\d{1,3})$`
)

// amountMessage states what a decimal field accepts, for the client to act
// on.
func amountMessage(places int32) string {
	if places == 3 {
		return "enter a number with up to three decimal places, using . or , — such as 1.250"
	}
	return "enter a number with up to two decimal places, using . or , — such as 351.90"
}

// amountError is the rejection of one decimal field: Invalid, naming the
// field as the client sent it and stating what it accepts.
func amountError(field string, places int32) error {
	return apperr.New(apperr.Invalid, "invalid amount").WithField(field, amountMessage(places))
}

// parseAmount normalises one decimal input field: surrounding spaces are
// trimmed, a decimal comma becomes a dot, and the value must stay within
// the integer and decimal bounds of its kind. A nil or blank value is null
// when the field is not required; a required one is rejected. Every
// rejection names the field and states the accepted form.
func parseAmount(value *string, places int32, field string, required bool) (decimal.NullDecimal, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		if required {
			return decimal.NullDecimal{}, amountError(field, places)
		}
		return decimal.NullDecimal{}, nil
	}
	text := strings.TrimSpace(*value)
	if strings.Contains(text, ".") && strings.Contains(text, ",") {
		return decimal.NullDecimal{}, amountError(field, places)
	}
	pattern := moneyPattern
	if places == 3 {
		pattern = quantityPattern
	}
	normalised := strings.ReplaceAll(text, ",", ".")
	if !regexp.MustCompile(pattern).MatchString(normalised) {
		return decimal.NullDecimal{}, amountError(field, places)
	}
	number, err := decimal.NewFromString(normalised)
	if err != nil {
		return decimal.NullDecimal{}, amountError(field, places)
	}
	return decimal.NewNullDecimal(number), nil
}
