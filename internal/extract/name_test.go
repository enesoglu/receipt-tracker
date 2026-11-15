package extract

import "testing"

func TestDisplayName(t *testing.T) {
	tests := []struct {
		raw, want string
	}{
		{"LAVAS 6?LI TAM BUGDA%01", "LAVAS 6?LI TAM BUGDA"},
		{"YOGURT YARIM YAGLI 3%01", "YOGURT YARIM YAGLI 3"},
		{"EKMEK %08", "EKMEK"},
		{"EKMEK %08.", "EKMEK"},
		{"SU 5L%20 ", "SU 5L"},
		{"SUT %3", "SUT %3"},
		{"SUT %1,5 YAGLI", "SUT %1,5 YAGLI"},
		{"KOLA %10 SEKERSIZ", "KOLA %10 SEKERSIZ"},
		{"PEYNIR", "PEYNIR"},
		{"%01", "%01"},
		{"  AYRAN  ", "AYRAN"},
	}
	for _, tt := range tests {
		if got := DisplayName(tt.raw); got != tt.want {
			t.Errorf("DisplayName(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
