package matching

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// The golden cases in testdata/golden.json are synthetic receipt lines
// modelled on Turkish grocery receipts. Each case lists the products the
// user already has, an incoming line and the decision §5.2 requires.
//
// Names are written as printed; the test stores aliases and rejections in
// normalised form, as the database does. Products and stores are named
// symbolically. Within a case, product ids follow the alphabetical order
// of the symbolic names, so where Decide breaks a tie by product id the
// expected order is alphabetical.
//
// Rules 1 and 2 never decide on their own. A case lists them in "covers"
// when it shows them changing the outcome, and the test checks that.

type goldenCase struct {
	Name       string            `json:"name"`
	Covers     []int             `json:"covers"`
	Products   []goldenProduct   `json:"products"`
	Rejections []goldenRejection `json:"rejections"`
	Line       goldenLine        `json:"line"`
	Want       goldenWant        `json:"want"`
}

type goldenProduct struct {
	ID      string        `json:"id"`
	Aliases []goldenAlias `json:"aliases"`
	Codes   []goldenCode  `json:"codes"`
	Size    string        `json:"size"` // recorded on the product: "1 l"
}

type goldenAlias struct {
	Name  string `json:"name"`
	Store string `json:"store"`
}

type goldenCode struct {
	Code  string `json:"code"`
	Kind  string `json:"kind"`
	Store string `json:"store"`
}

type goldenRejection struct {
	Product string `json:"product"`
	Name    string `json:"name"`
}

type goldenLine struct {
	Name  string `json:"name"`
	Code  string `json:"code"`
	Store string `json:"store"`
}

type goldenWant struct {
	Outcome    Outcome  `json:"outcome"`
	Rule       int      `json:"rule"`
	Reason     Reason   `json:"reason"`
	Product    string   `json:"product"`
	Candidates []string `json:"candidates"`
}

func TestGolden(t *testing.T) {
	cases := loadGolden(t)
	if len(cases) < 80 {
		t.Errorf("golden set has %d cases, want at least 80", len(cases))
	}
	byRule := make(map[int][]bool) // rule → pass/fail per case
	for _, gc := range cases {
		passed := t.Run(gc.Name, func(t *testing.T) { runGolden(t, gc) })
		byRule[gc.Want.Rule] = append(byRule[gc.Want.Rule], passed)
		for _, rule := range gc.Covers {
			byRule[rule] = append(byRule[rule], passed)
		}
	}

	t.Logf("golden summary: %d cases", len(cases))
	for rule := 1; rule <= 10; rule++ {
		results := byRule[rule]
		passed := 0
		for _, ok := range results {
			if ok {
				passed++
			}
		}
		role := "decides"
		if rule <= 2 {
			role = "changes the outcome"
		}
		t.Logf("  rule %2d %-20s %3d cases, %3d passed", rule, role, len(results), passed)
		if len(results) == 0 {
			t.Errorf("no golden case for rule %d", rule)
		}
	}
}

func runGolden(t *testing.T, gc goldenCase) {
	ids := goldenProductIDs(gc)
	line, candidates, rejections := goldenInput(t, gc, ids)
	got := Decide(line, candidates, rejections)

	names := make(map[uuid.UUID]string, len(ids))
	for name, id := range ids {
		names[id] = name
	}
	gotCandidates := make([]string, 0, len(got.Candidates))
	for _, c := range got.Candidates {
		gotCandidates = append(gotCandidates, names[c.ProductID])
	}
	gotProduct := names[got.ProductID]
	if got.Outcome != gc.Want.Outcome || got.Rule != gc.Want.Rule || got.Reason != gc.Want.Reason || gotProduct != gc.Want.Product {
		t.Errorf("decision = %s rule %d %s %q, want %s rule %d %s %q",
			got.Outcome, got.Rule, got.Reason, gotProduct,
			gc.Want.Outcome, gc.Want.Rule, gc.Want.Reason, gc.Want.Product)
	}
	if diff := cmp.Diff(gc.Want.Candidates, gotCandidates); diff != "" {
		t.Errorf("candidates mismatch (-want +got):\n%s", diff)
	}
	if t.Failed() {
		t.Logf("line key %q; scored candidates:", Normalize(gc.Line.Name).Key)
		for _, c := range got.Candidates {
			t.Logf("  %-24s score %.4f owns code %v blocked %v", names[c.ProductID], c.Score, c.OwnsCode, c.Blocked)
		}
	}

	for _, rule := range gc.Covers {
		switch rule {
		case 1:
			// Without the rejections the decision must be different.
			without := Decide(line, candidates, nil)
			if without.Outcome == got.Outcome && without.Rule == got.Rule && without.ProductID == got.ProductID {
				t.Errorf("covers rule 1, but the decision is the same without the rejections")
			}
		case 2:
			if !sizeRuleMattered(got) {
				t.Errorf("covers rule 2, but no offered candidate shows a blocked product changing the decision")
			}
		default:
			t.Errorf("covers lists rule %d; only the filter rules 1 and 2 belong there", rule)
		}
	}
}

// sizeRuleMattered reports whether a blocked product changed the decision:
// the reason became a size conflict, or a blocked product would have been
// linked (by code or by name) or would have been a rival without rule 2.
func sizeRuleMattered(d Decision) bool {
	if d.Reason == ReasonSizeConflict {
		return true
	}
	for _, c := range d.Candidates {
		if c.Blocked && (reaches(c.Score, autoLinkScore) || c.OwnsCode && reaches(c.Score, codeMinScore)) {
			return true
		}
	}
	return false
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatalf("read golden cases: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := decoder.Decode(&file); err != nil {
		t.Fatalf("parse golden cases: %v", err)
	}
	seen := make(map[string]bool)
	for _, gc := range file.Cases {
		if gc.Name == "" || seen[gc.Name] {
			t.Fatalf("golden case name %q is empty or used twice", gc.Name)
		}
		seen[gc.Name] = true
	}
	return file.Cases
}

// goldenProductIDs gives every product of a case an id; the ids sort like
// the symbolic names.
func goldenProductIDs(gc goldenCase) map[string]uuid.UUID {
	names := make([]string, 0, len(gc.Products))
	for _, p := range gc.Products {
		names = append(names, p.ID)
	}
	slices.Sort(names)
	ids := make(map[string]uuid.UUID, len(names))
	for i, name := range names {
		var id uuid.UUID
		binary.BigEndian.PutUint64(id[8:], uint64(i+1))
		ids[name] = id
	}
	return ids
}

func goldenInput(t *testing.T, gc goldenCase, ids map[string]uuid.UUID) (Line, []Candidate, []Rejection) {
	t.Helper()
	if len(ids) != len(gc.Products) {
		t.Fatalf("a product id is used twice")
	}
	line := Line{Name: gc.Line.Name, Code: gc.Line.Code, StoreID: storeID(gc.Line.Store)}
	candidates := make([]Candidate, 0, len(gc.Products))
	for _, p := range gc.Products {
		c := Candidate{ProductID: ids[p.ID]}
		for _, a := range p.Aliases {
			c.Aliases = append(c.Aliases, Alias{Name: Normalize(a.Name).Key, StoreID: storeID(a.Store)})
		}
		for _, code := range p.Codes {
			kind := CodeKind(code.Kind)
			if kind != CodeGTIN && kind != CodeStore || (kind == CodeStore) != (code.Store != "") {
				t.Fatalf("product %q: code %+v needs kind gtin without a store, or kind store with one", p.ID, code)
			}
			c.Codes = append(c.Codes, Code{Value: code.Code, Kind: kind, StoreID: storeID(code.Store)})
		}
		if p.Size != "" {
			value, unit, _ := strings.Cut(p.Size, " ")
			c.Size = &Size{Value: decimal.RequireFromString(value), Unit: unit}
		}
		candidates = append(candidates, c)
	}
	rejections := make([]Rejection, 0, len(gc.Rejections))
	for _, r := range gc.Rejections {
		id, ok := ids[r.Product]
		if !ok {
			t.Fatalf("rejection names unknown product %q", r.Product)
		}
		rejections = append(rejections, Rejection{ProductID: id, Name: Normalize(r.Name).Key})
	}
	for _, name := range append([]string{gc.Want.Product}, gc.Want.Candidates...) {
		if _, ok := ids[name]; name != "" && !ok {
			t.Fatalf("want names unknown product %q", name)
		}
	}
	return line, candidates, rejections
}
