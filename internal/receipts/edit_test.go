package receipts

import (
	"testing"

	"github.com/shopspring/decimal"
)

// validReceiptInput is the shape every other field already agrees on; the
// identity fields vary per case.
func validReceiptInput() ReceiptInput {
	value := "18:30"
	return ReceiptInput{Title: "Home shopping", Currency: "TRY", Total: decimal.NewNullDecimal(decimal.RequireFromString("10.00")), PurchasedTime: &value}
}

func TestValidateReceipt_IdentityFields(t *testing.T) {
	tests := []struct {
		name string
		set  func(*ReceiptInput)
		want bool
	}{
		{"no identity is fine", func(*ReceiptInput) {}, true},
		{
			"printed invoice number",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("T112026000000000")
			},
			true,
		},
		{
			"invoice number with a space around it",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr(" T112026000000000 ")
			},
			true,
		},
		{
			"invoice number with punctuation",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("T1-2")
			},
			false,
		},
		{
			"lower case invoice number",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("t112026000000000")
			},
			false,
		},
		{
			"thirty three character invoice number",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("T112345678901234567890123456789012")
			},
			false,
		},
		{
			"blank invoice number",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("   ")
			},
			false,
		},
		{
			"ten digit tax number",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr("1234567890")
			},
			true,
		},
		{
			"a tax number failing the check digit is still a correction",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr("1750051847")
			},
			true,
		},
		{
			"tax number with a space around it",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr(" 1234567890 ")
			},
			true,
		},
		{
			"nine digit tax number",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr("123456789")
			},
			false,
		},
		{
			"tax number with a letter",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr("12345678AB")
			},
			false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validReceiptInput()
			test.set(&input)
			err := validateReceipt(input)
			if (err == nil) != test.want {
				t.Fatalf("validateReceipt() error = %v, want error: %v", err, !test.want)
			}
		})
	}
}

func ptr(value string) *string { return &value }
