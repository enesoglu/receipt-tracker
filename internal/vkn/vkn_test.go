package vkn

import "testing"

func TestValid_AcceptsWellFormed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
	}{
		// The first two are public corporate VKNs printed on every receipt
		// of their chains; the rest are synthetic or published examples.
		{"chain vkn with letter-free digits", "1750051846"},
		{"chain vkn with zero pair", "9480423762"},
		{"synthetic sequence", "1234567890"},
		{"synthetic leading zero", "0123456789"},
		{"published example", "4840847211"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !Valid(test.value) {
				t.Errorf("Valid(%q) = false, want true", test.value)
			}
		})
	}
}

func TestValid_RejectsMisreads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
	}{
		{"wrong check digit", "1750051847"},
		{"transposed digits", "1750051864"},
		{"nine digits", "175005184"},
		{"eleven digits", "17500518461"},
		{"letters", "17500518AB"},
		{"spaces around", " 1750051846 "},
		{"space inside", "1750 51846"},
		{"empty", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if Valid(test.value) {
				t.Errorf("Valid(%q) = true, want false", test.value)
			}
		})
	}
}
