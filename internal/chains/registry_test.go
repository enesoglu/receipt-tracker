package chains

import (
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/matching"
	"github.com/enesoglu/receipt-tracker/internal/vkn"
)

func TestAll_SeedsFourChains(t *testing.T) {
	t.Parallel()
	all := All()
	if len(all) != 4 {
		t.Fatalf("All() has %d chains, want 4", len(all))
	}
	ids := map[string]bool{}
	for _, chain := range all {
		if ids[chain.ID] {
			t.Errorf("duplicate chain id %q", chain.ID)
		}
		ids[chain.ID] = true
		if chain.Name == "" || len(chain.Keywords) == 0 {
			t.Errorf("chain %q has no name or keywords", chain.ID)
		}
		// The nil rule is "no portal known"; every seeded chain has one.
		if chain.EArchive == nil {
			t.Errorf("chain %q has no e-archive entry, want at least a portal", chain.ID)
		}
	}
}

func TestAll_VKNsAreValidTaxNumbers(t *testing.T) {
	t.Parallel()
	for _, chain := range All() {
		for _, number := range chain.VKNs {
			if !vkn.Valid(number) {
				t.Errorf("chain %q lists VKN %q, which fails the check digit", chain.ID, number)
			}
		}
	}
}

// Keywords are stored in the comparison-key form of matching.Normalize, so
// store resolution can compare them to a receipt's store name without
// normalising again. Only this test imports matching; the package itself
// stays dependency-free.
func TestAll_KeywordsAreComparisonKeys(t *testing.T) {
	t.Parallel()
	for _, chain := range All() {
		for _, keyword := range chain.Keywords {
			if got := matching.Normalize(keyword).Key; got != keyword {
				t.Errorf("chain %q keyword %q is not a comparison key, Normalize gives %q", chain.ID, keyword, got)
			}
		}
	}
}

func TestByVKN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		vkn  string
		// want is the chain id, empty when no chain may resolve.
		want string
	}{
		{"bim seller", "1750051846", "bim"},
		{"a101 seller", "9480423762", "a101"},
		{"valid but unknown seller", "1234567890", ""},
		{"empty", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			chain, ok := ByVKN(test.vkn)
			if test.want == "" {
				if ok {
					t.Fatalf("ByVKN(%q) resolved chain %q, want none", test.vkn, chain.ID)
				}
				return
			}
			if !ok || chain.ID != test.want {
				t.Errorf("ByVKN(%q) = %q, %v; want %q", test.vkn, chain.ID, ok, test.want)
			}
		})
	}
}

func TestAll_DemonstratedAndInspectedPortals(t *testing.T) {
	t.Parallel()
	all := map[string]Chain{}
	for _, chain := range All() {
		all[chain.ID] = chain
	}
	bim := all["bim"].EArchive
	if !bim.Verified || bim.Captcha != CaptchaNone || bim.Endpoint == "" || len(bim.Fields) != 3 {
		t.Errorf("BİM portal = %+v, want the demonstrated lookup with three fields and no captcha", bim)
	}
	a101 := all["a101"].EArchive
	if a101.Verified || a101.Captcha != CaptchaTurnstile || a101.Endpoint == "" || len(a101.Fields) != 4 {
		t.Errorf("A101 portal = %+v, want the inspected Turnstile-guarded form with four fields", a101)
	}
	for _, id := range []string{"sok", "migros"} {
		portal := all[id].EArchive
		if portal.Verified || portal.Captcha != CaptchaImage || portal.Endpoint != "" || len(portal.Fields) != 0 {
			t.Errorf("%s portal = %+v, want an image-captcha landing page with no recorded fields", id, portal)
		}
	}
}
