package httpapi

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/chains"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

func textPointer(value string) *string { return &value }

func TestChainLookupValue(t *testing.T) {
	date := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	ettn := uuid.MustParse("1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d")
	cases := []struct {
		name      string
		receipt   sqlc.Receipt
		field     string
		want      *string
		wantKnown bool
	}{
		{
			name:      "belgeno prefers the invoice number",
			receipt:   sqlc.Receipt{InvoiceNumber: textPointer("T112026000000000"), Ettn: uuid.NullUUID{UUID: ettn, Valid: true}},
			field:     "belgeno",
			want:      textPointer("T112026000000000"),
			wantKnown: true,
		},
		{
			name:      "belgeno falls back to the canonical ETTN",
			receipt:   sqlc.Receipt{Ettn: uuid.NullUUID{UUID: ettn, Valid: true}},
			field:     "belgeno",
			want:      textPointer(ettn.String()),
			wantKnown: true,
		},
		{
			name:      "belgeno is null when both are missing",
			receipt:   sqlc.Receipt{},
			field:     "belgeno",
			want:      nil,
			wantKnown: true,
		},
		{
			name:      "tarih is formatted DD-MM-YYYY",
			receipt:   sqlc.Receipt{PurchasedOn: &date},
			field:     "tarih",
			want:      textPointer("05-03-2026"),
			wantKnown: true,
		},
		{
			name:      "tarih is null without a purchase date",
			receipt:   sqlc.Receipt{},
			field:     "tarih",
			want:      nil,
			wantKnown: true,
		},
		{
			name:      "tutar passes the canonical total through unchanged",
			receipt:   sqlc.Receipt{Total: decimal.NullDecimal{Decimal: decimal.RequireFromString("129.40"), Valid: true}},
			field:     "tutar",
			want:      textPointer("129.40"),
			wantKnown: true,
		},
		{
			name:      "tutar is null without a total",
			receipt:   sqlc.Receipt{},
			field:     "tutar",
			want:      nil,
			wantKnown: true,
		},
		{
			name:      "an unknown wire name is not sourced",
			receipt:   sqlc.Receipt{},
			field:     "recaptcha",
			want:      nil,
			wantKnown: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := lookupValue(tc.receipt, tc.field)
			if known != tc.wantKnown {
				t.Errorf("known=%v want %v", known, tc.wantKnown)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("value mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestChainResponseRequiresKnownVKN(t *testing.T) {
	for _, tc := range []struct {
		name string
		vkn  *string
	}{
		{name: "missing VKN", vkn: nil},
		{name: "unknown VKN", vkn: textPointer("9999999999")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chainResponse(sqlc.Receipt{SellerTaxNumber: tc.vkn}); got != nil {
				t.Fatalf("chain=%+v, want nil", got)
			}
		})
	}
}

func TestChainResponseBimValues(t *testing.T) {
	date := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	receipt := sqlc.Receipt{
		SellerTaxNumber: textPointer("1750051846"),
		InvoiceNumber:   textPointer("BIM2026001234"),
		PurchasedOn:     &date,
		Total:           decimal.NullDecimal{Decimal: decimal.RequireFromString("129.40"), Valid: true},
	}
	want := &ReceiptChain{
		Id:        "bim",
		Name:      "BİM",
		PortalUrl: "https://arsiv.bim.com.tr/",
		Captcha:   ReceiptChainCaptchaNone,
		Fields: []ReceiptChainField{
			{Name: "belgeno", Description: "Invoice number, ETTN or barcode/reference", Value: textPointer("BIM2026001234")},
			{Name: "tarih", Description: "Invoice date", Value: textPointer("05-03-2026")},
			{Name: "tutar", Description: "Total paid", Value: textPointer("129.40")},
		},
	}
	if diff := cmp.Diff(want, chainResponse(receipt)); diff != "" {
		t.Errorf("chain mismatch (-want +got):\n%s", diff)
	}
}

func TestChainLookupDropsRecaptcha(t *testing.T) {
	a101, ok := chains.ByVKN("9480423762")
	if !ok {
		t.Fatal("A101 missing from registry")
	}
	chain := chainLookup(sqlc.Receipt{}, a101)
	if len(chain.Fields) != 3 {
		t.Fatalf("fields=%+v, want the three sourceable fields", chain.Fields)
	}
	for _, field := range chain.Fields {
		if field.Name == "recaptcha" {
			t.Errorf("captcha token leaked into fields: %+v", field)
		}
	}
	if chain.Captcha != ReceiptChainCaptchaTurnstile {
		t.Errorf("captcha=%q, want turnstile", chain.Captcha)
	}
}

func TestChainLookupWithoutKnownFields(t *testing.T) {
	empty := chains.Chain{
		ID:   "sok",
		Name: "Şok",
		EArchive: &chains.EArchive{
			PortalURL: "https://earsiv.sokmarket.com.tr/",
			Captcha:   chains.CaptchaImage,
		},
	}
	chain := chainLookup(sqlc.Receipt{}, empty)
	if chain.Fields == nil || len(chain.Fields) != 0 {
		t.Errorf("fields=%+v, want an empty slice", chain.Fields)
	}
	if chain.Captcha != ReceiptChainCaptchaImage {
		t.Errorf("captcha=%q, want image", chain.Captcha)
	}
}
