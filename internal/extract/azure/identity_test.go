package azure

import (
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/extract"
)

// foundText and foundUUID build the expected identity fields; the reader
// reports no provider confidence, because the printed text carries none.
func foundText(value string) extract.Field[string] {
	return extract.Field[string]{Value: value, Found: true}
}

func foundUUID(value string) extract.Field[uuid.UUID] {
	return extract.Field[uuid.UUID]{Value: uuid.MustParse(value), Found: true}
}

// Every value below is invented: synthetic numbers that hold their printed
// shape and tax numbers whose check digit passes.
func TestParse_ReadsIdentityFromStoredRaw(t *testing.T) {
	raw, err := os.ReadFile("testdata/receipt-identity.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.InvoiceNumber != foundText("T112026000000000") {
		t.Errorf("invoice number = %+v, want T112026000000000", got.InvoiceNumber)
	}
	if got.Ettn != foundUUID("1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d") {
		t.Errorf("ettn = %+v, want 1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", got.Ettn)
	}
	if got.SellerTaxNumber != foundText("1234567890") {
		t.Errorf("seller tax number = %+v, want 1234567890", got.SellerTaxNumber)
	}
	if !got.StoreName.Found || !got.Total.Found {
		t.Errorf("ordinary receipt fields were lost: %+v", got)
	}
}

func TestParse_IdentityFromCompactPrint(t *testing.T) {
	raw, err := os.ReadFile("testdata/receipt-identity-compact.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The BELGE value loses the space a long digit run picked up, the
	// compact ETTN gains its hyphens, and the V.K.N label reads like VKN.
	if got.InvoiceNumber != foundText("11223344556677889900") {
		t.Errorf("invoice number = %+v, want 11223344556677889900", got.InvoiceNumber)
	}
	if got.Ettn != foundUUID("a1b2c3d4-e5f6-0718-293a-4b5c6d7e8f90") {
		t.Errorf("ettn = %+v, want a1b2c3d4-e5f6-0718-293a-4b5c6d7e8f90", got.Ettn)
	}
	if got.SellerTaxNumber != foundText("0123456789") {
		t.Errorf("seller tax number = %+v, want 0123456789", got.SellerTaxNumber)
	}
}

// The want fields hold the expected value, with "" for one that must come
// back not found.
func TestReadIdentity(t *testing.T) {
	tests := []struct {
		name                           string
		content                        string
		wantInvoice, wantEttn, wantTax string
	}{
		{
			name:    "vergi no with dotted capital I",
			content: "VERGİ NO: 0123456789",
			wantTax: "0123456789",
		},
		{
			name:        "lowercase labels and lowercase digits",
			content:     "fatura no: t112026000000000\nettn: 0a1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5d",
			wantInvoice: "T112026000000000",
			wantEttn:    "0a1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5d",
		},
		{
			name:    "tax number failing the check digit stays unreadable",
			content: "VKN: 1750051847",
		},
		{
			name:    "nine digit tax number is not read",
			content: "VKN: 123456789",
		},
		{
			name:    "spaced tax number is recovered",
			content: "V.K.N: 012 345 6789",
			wantTax: "0123456789",
		},
		{
			name:    "the buyer tckn beside a vkn label is skipped for the seller vkn",
			content: "TCKN/VKN: 99999999999-ALICI\nVERGI NO: 0123456789",
			wantTax: "0123456789",
		},
		{
			name:    "spaced digits that are not ten are not read",
			content: "VKN: 123 456 789",
		},
		{
			name:    "a spaced tax number failing the check digit is a misread",
			content: "V.K.N: 175 005 1847",
		},
		{
			name:    "a broken fatura candidate is not read",
			content: "FATURA NO: T112026000",
		},
		{
			name:     "the leading O repair",
			content:  "ETTNOa2b3c4d5e6f4a7b8c9d0e1f2a3b4c5d",
			wantEttn: "0a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d",
		},
		{
			name:    "an O inside the digits is not repaired",
			content: "ETTN: 1a2b3c4dO5e6f4a7b8c9d0e1f2a3b4c5d",
		},
		{
			name:     "the earlier ettn print wins over a later different shape",
			content:  "ETTN: A1B2C3D4E5F60718293A4B5C6D7E8F90\nNOT ETTN: 1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d",
			wantEttn: "a1b2c3d4-e5f6-0718-293a-4b5c6d7e8f90",
		},
		{
			name:    "a receipt without the labels is ordinary",
			content: "MAYONEZ 840G BOLBOL\n149,00",
		},
		{
			name:    "whitespace only",
			content: "  \n\t ",
		},
		{
			name:        "the earlier invoice label wins",
			content:     "BELGE NO: 11223344556677889900\nARADAN BIR SATIR\nFATURA NO: T112026000000000",
			wantInvoice: "11223344556677889900",
		},
		{
			name:        "belge barkodu variant",
			content:     "BELGE BARKODU: 11223344556677889900",
			wantInvoice: "11223344556677889900",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invoice, ettn, tax := readIdentity(test.content)
			if test.wantInvoice == "" {
				if invoice.Found {
					t.Errorf("invoice number = %+v, want not found", invoice)
				}
			} else if invoice.Value != test.wantInvoice {
				t.Errorf("invoice number = %q, want %q", invoice.Value, test.wantInvoice)
			}
			if test.wantEttn == "" {
				if ettn.Found {
					t.Errorf("ettn = %+v, want not found", ettn)
				}
			} else if got := ettn.Value.String(); got != test.wantEttn {
				t.Errorf("ettn = %q, want %q", got, test.wantEttn)
			}
			if test.wantTax == "" {
				if tax.Found {
					t.Errorf("seller tax number = %+v, want not found", tax)
				}
			} else if tax.Value != test.wantTax {
				t.Errorf("seller tax number = %q, want %q", tax.Value, test.wantTax)
			}
		})
	}
}

func TestReadIdentity_IsDeterministic(t *testing.T) {
	content := "FATURA NO: T112026000000000\nETTN: 1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d\nVKN: 1234567890"
	firstInvoice, firstEttn, firstTax := readIdentity(content)
	secondInvoice, secondEttn, secondTax := readIdentity(content)
	if firstInvoice != secondInvoice || firstEttn != secondEttn || firstTax != secondTax {
		t.Errorf("repeated reads disagree: %+v %+v %+v vs %+v %+v %+v",
			firstInvoice, firstEttn, firstTax, secondInvoice, secondEttn, secondTax)
	}
}
