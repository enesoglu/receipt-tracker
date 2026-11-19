package receipts

import (
	"strings"
	"testing"
)

func TestBranchFrom(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		token string
		want  *string
	}{
		{
			"registry name stripped, branch keeps its case",
			"BİM ANKARA ÇANKAYA", "bim", ptr("ANKARA ÇANKAYA"),
		},
		{
			"dotted i variants fold to the same token",
			"BIM ANKARA", "bim", ptr("ANKARA"),
		},
		{
			"multi-word keyword",
			"A 101 ANKARA", "a 101", ptr("ANKARA"),
		},
		{
			"keyword folded across punctuation",
			"A-101 ANKARA", "a 101", ptr("ANKARA"),
		},
		{
			"separators trimmed from the branch",
			"BİM / ANKARA -", "bim", ptr("ANKARA"),
		},
		{
			"only the chain token",
			"BİM", "bim", nil,
		},
		{
			"token the name does not spell",
			"Huzur Yenimahall", "bim", ptr("Huzur Yenimahall"),
		},
		{
			"empty name",
			"", "bim", nil,
		},
		{
			"over-long remainder",
			"bim " + strings.Repeat("a", 201), "bim", nil,
		},
		{
			"inner nul byte is unreadable",
			"BİM AN\x00KARA", "bim", nil,
		},
		{
			"no token leaves the name whole",
			"ANKARA ÇANKAYA", "", ptr("ANKARA ÇANKAYA"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := branchFrom(tt.raw, tt.token); (got == nil) != (tt.want == nil) ||
				got != nil && tt.want != nil && *got != *tt.want {
				t.Errorf("branchFrom(%q, %q) = %v, want %v", tt.raw, tt.token, got, tt.want)
			}
		})
	}
}
