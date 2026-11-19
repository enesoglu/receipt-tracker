// Package chains is the retail-chain registry: static facts about the
// grocery chains whose receipts this app reads — the seller tax numbers
// their receipts print, the keywords their store names contain, and how
// their e-archive invoice portals accept a lookup. Store resolution and
// the e-archive panel consume it; the registry owns no I/O and imports
// nothing outside the standard library, so any package can use it without
// dragging dependencies along. Entries hold only what the earsiv probe
// established: corporate identifiers printed on every receipt of a chain
// and portal behaviour observed or read from the portal's own page code,
// never anything from a particular person's receipt.
package chains

// Version is the content revision of the registry. Bump it whenever an
// entry changes, so logs and tests can tell seed generations apart.
const Version = 1

// CaptchaKind names the challenge a portal puts in front of a lookup.
type CaptchaKind string

// The observed kinds. None means the demonstrated flow needs no challenge.
const (
	CaptchaNone      CaptchaKind = "none"
	CaptchaImage     CaptchaKind = "image"
	CaptchaTurnstile CaptchaKind = "turnstile"
)

// LookupField is one wire field of a portal lookup: the name the form POST
// carries, what the user enters into it, and the format the portal expects
// on the wire.
type LookupField struct {
	Name        string
	Description string
	Format      string
}

// EArchive is what is known about one chain's e-archive invoice portal.
// Endpoint and Fields are empty when only the landing page was observed.
// Verified is true only when a full lookup was demonstrated, never for a
// wire format merely read out of the portal's own page code.
type EArchive struct {
	PortalURL string
	Endpoint  string
	Fields    []LookupField
	Captcha   CaptchaKind
	Verified  bool
}

// Chain is one retail chain. VKNs are the seller tax numbers printed on
// its receipts — public corporate identifiers, not anyone's personal data.
// Keywords are provisional, the chain's own name, and are stored in the
// comparison-key form of matching.Normalize so consumers can compare them
// to a store name as-is; the package itself stays dependency-free and a
// test pins the form. EArchive is nil only for a chain with no known
// portal.
type Chain struct {
	ID       string
	Name     string
	VKNs     []string
	Keywords []string
	EArchive *EArchive
}

// All returns every registered chain. The seed is a literal rebuilt on
// each call, so a caller can never mutate what another caller sees.
func All() []Chain {
	return []Chain{
		{
			ID:       "a101",
			Name:     "A101",
			VKNs:     []string{"9480423762"},
			Keywords: []string{"a 101"},
			EArchive: &EArchive{
				PortalURL: "https://fatura.a101.com.tr/",
				Endpoint:  "Viewer/FaturaGetir",
				Fields: []LookupField{
					{Name: "belgeno", Description: "Document number, ETTN or invoice number", Format: "printed"},
					{Name: "tarih", Description: "Invoice date", Format: "DD-MM-YYYY"},
					{Name: "tutar", Description: "Total paid", Format: "decimal, dot separator"},
					{Name: "recaptcha", Description: "Cloudflare Turnstile token", Format: "token"},
				},
				Captcha: CaptchaTurnstile,
				// Static inspection of the page code only: the probe stopped
				// at the challenge and no lookup was submitted.
				Verified: false,
			},
		},
		{
			ID:       "bim",
			Name:     "BİM",
			VKNs:     []string{"1750051846"},
			Keywords: []string{"bim"},
			EArchive: &EArchive{
				PortalURL: "https://arsiv.bim.com.tr/",
				Endpoint:  "Viewer/FaturaGetir",
				Fields: []LookupField{
					{Name: "belgeno", Description: "Invoice number, ETTN or barcode/reference", Format: "printed"},
					{Name: "tarih", Description: "Invoice date", Format: "DD-MM-YYYY"},
					{Name: "tutar", Description: "Total paid", Format: "decimal, comma replaced by a dot"},
				},
				Captcha: CaptchaNone,
				// A full lookup was demonstrated against a real invoice.
				Verified: true,
			},
		},
		{
			ID:       "sok",
			Name:     "Şok",
			Keywords: []string{"sok"},
			EArchive: &EArchive{
				PortalURL: "https://earsiv.sokmarket.com.tr/",
				Captcha:   CaptchaImage,
				Verified:  false,
			},
		},
		{
			ID:       "migros",
			Name:     "Migros",
			Keywords: []string{"migros"},
			EArchive: &EArchive{
				PortalURL: "https://earsiv.migros.com.tr/",
				Captcha:   CaptchaImage,
				Verified:  false,
			},
		},
	}
}

// ByVKN returns the chain whose receipts print the seller tax number vkn.
func ByVKN(vkn string) (Chain, bool) {
	for _, chain := range All() {
		for _, known := range chain.VKNs {
			if known == vkn {
				return chain, true
			}
		}
	}
	return Chain{}, false
}
