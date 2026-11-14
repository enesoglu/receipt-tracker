package matching

import "testing"

func TestValidGTIN(t *testing.T) {
	// The valid codes are synthetic: their check digits were computed for
	// this test and they do not name real products.
	tests := []struct {
		name string
		code string
		want bool
	}{
		{"GTIN-13", "8690000000012", true},
		{"GTIN-13 check digit zero", "8691234567890", true},
		{"GTIN-12", "012345678905", true},
		{"GTIN-8", "96385074", true},
		{"GTIN-13 wrong check digit", "8690000000013", false},
		{"GTIN-12 wrong check digit", "012345678900", false},
		{"GTIN-8 wrong check digit", "96385075", false},
		{"two digits swapped", "8691234567980", false},
		{"store code length 6", "123456", false},
		{"GTIN-14 is not accepted", "18690000000012", false},
		{"letter in the body", "86900A0000012", false},
		{"letter as check digit", "869000000001X", false},
		{"space", "8690000 00012", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidGTIN(tt.code); got != tt.want {
				t.Errorf("ValidGTIN(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}
