package matching

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
)

// fixture writes synthetic users, stores, products, aliases, codes and
// rejections. The queries that write them belong to other packages, so the
// test inserts rows directly.
type fixture struct {
	t    testing.TB
	pool *pgxpool.Pool
}

func newFixture(t testing.TB) fixture {
	return fixture{t: t, pool: dbtest.New(t)}
}

func (f fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatalf("seed: %v\n%s", err, sql)
	}
}

func (f fixture) user(name string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.t.Context(),
		`INSERT INTO users (google_sub, email) VALUES ($1, $2) RETURNING id`,
		"sub-"+name, name+"@example.com").Scan(&id)
	if err != nil {
		f.t.Fatalf("seed user: %v", err)
	}
	return id
}

func (f fixture) store(user uuid.UUID, name string) uuid.NullUUID {
	f.t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.t.Context(),
		`INSERT INTO stores (user_id, name, normalized_name) VALUES ($1, $2, $2) RETURNING id`,
		user, name).Scan(&id)
	if err != nil {
		f.t.Fatalf("seed store: %v", err)
	}
	return uuid.NullUUID{UUID: id, Valid: true}
}

// product inserts a product with the given id, or a generated one when id
// is uuid.Nil.
func (f fixture) product(user, id uuid.UUID, name string, size *Size) uuid.UUID {
	f.t.Helper()
	if id == uuid.Nil {
		id = uuid.New()
	}
	value, unit := decimal.NullDecimal{}, (*string)(nil)
	if size != nil {
		value, unit = decimal.NullDecimal{Decimal: size.Value, Valid: true}, &size.Unit
	}
	f.exec(`INSERT INTO products (id, user_id, canonical_name, size_value, size_unit) VALUES ($1, $2, $3, $4, $5)`,
		id, user, name, value, unit)
	return id
}

func (f fixture) alias(user, product uuid.UUID, raw string, store uuid.NullUUID) {
	f.t.Helper()
	f.exec(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name, store_id) VALUES ($1, $2, $3, $4, $5)`,
		user, product, raw, Normalize(raw).Key, store)
}

func (f fixture) code(user, product uuid.UUID, code string, kind CodeKind, store uuid.NullUUID) {
	f.t.Helper()
	f.exec(`INSERT INTO product_codes (user_id, product_id, code, kind, store_id) VALUES ($1, $2, $3, $4, $5)`,
		user, product, code, string(kind), store)
}

func (f fixture) rejection(user, product uuid.UUID, raw string) {
	f.t.Helper()
	f.exec(`INSERT INTO match_rejections (user_id, product_id, normalized_name) VALUES ($1, $2, $3)`,
		user, product, Normalize(raw).Key)
}

func TestMatchLine_NeverReturnsAnotherUsersProduct(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	aliceStore, bobStore := f.store(alice, "migros"), f.store(bob, "migros")

	// Alice knows the milk by name, barcode and store code; Bob knows
	// nothing yet.
	milk := f.product(alice, uuid.Nil, "Pinar Sut 1 L", nil)
	f.alias(alice, milk, "PINAR SUT 1 LT", aliceStore)
	f.code(alice, milk, gtinMilk, CodeGTIN, uuid.NullUUID{})
	f.code(alice, milk, storeCodeID, CodeStore, aliceStore)
	matcher := New(NewSQLSource(f.pool))

	for _, line := range []Line{
		{Name: "PINAR SUT 1LT", StoreID: bobStore},
		{Name: "SUT", Code: gtinMilk, StoreID: bobStore},
		{Name: "SUT", Code: storeCodeID, StoreID: bobStore},
	} {
		got, err := matcher.MatchLine(t.Context(), bob, line)
		if err != nil {
			t.Fatalf("MatchLine(bob, %+v) error = %v", line, err)
		}
		if got.Outcome != CreateNew || got.ProductID != uuid.Nil || len(got.Candidates) != 0 {
			t.Errorf("MatchLine(bob, %+v) = %+v, want a new product and no candidates", line, got)
		}
	}

	// Bob gets his own milk. Alice rejects her milk for the name, which
	// must not change Bob's match, and Bob's milk must not reach Alice.
	bobMilk := f.product(bob, uuid.Nil, "Sut", nil)
	f.alias(bob, bobMilk, "PINAR SUT 1 LT", bobStore)
	f.rejection(alice, milk, "PINAR SUT 1LT")

	got, err := matcher.MatchLine(t.Context(), bob, Line{Name: "PINAR SUT 1LT", StoreID: bobStore})
	if err != nil {
		t.Fatalf("MatchLine(bob) error = %v", err)
	}
	if got.Outcome != AutoLink || got.ProductID != bobMilk {
		t.Errorf("MatchLine(bob) = %+v, want his own milk linked", got)
	}
	got, err = matcher.MatchLine(t.Context(), alice, Line{Name: "PINAR SUT 1LT", StoreID: aliceStore})
	if err != nil {
		t.Fatalf("MatchLine(alice) error = %v", err)
	}
	for _, c := range got.Candidates {
		if c.ProductID == bobMilk {
			t.Errorf("MatchLine(alice) offered Bob's product: %+v", got)
		}
	}
	if got.Outcome != CreateNew {
		t.Errorf("MatchLine(alice) = %+v, want a new product after her rejection", got)
	}
}

func TestSQLSource_LimitsSimilarAliases(t *testing.T) {
	f := newFixture(t)
	user := f.user("alice")
	for i := range 30 {
		p := f.product(user, uuid.Nil, fmt.Sprintf("Ayran %d", i), nil)
		f.alias(user, p, fmt.Sprintf("AYRAN %d", i), uuid.NullUUID{})
	}
	exact := f.product(user, uuid.Nil, "Ayran", nil)
	f.alias(user, exact, "AYRAN", uuid.NullUUID{})

	got, _, err := NewSQLSource(f.pool).Candidates(t.Context(), user, "ayran", "")
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	aliases := 0
	for _, c := range got {
		aliases += len(c.Aliases)
	}
	if aliases != maxAliasCandidates {
		t.Errorf("Candidates() returned %d aliases, want %d", aliases, maxAliasCandidates)
	}
	if !slices.ContainsFunc(got, func(c Candidate) bool { return c.ProductID == exact }) {
		t.Errorf("Candidates() misses the exact alias among %d products", len(got))
	}
}

func TestSQLSource_AddsEveryProductThatOwnsTheCode(t *testing.T) {
	f := newFixture(t)
	user := f.user("alice")
	bim := f.store(user, "bim")
	// Twenty-five aliases more similar to the line than the code's owner,
	// so the owner is found only through its code.
	for i := range 25 {
		p := f.product(user, uuid.Nil, fmt.Sprintf("Kola %d", i), nil)
		f.alias(user, p, fmt.Sprintf("KOLA %d", i), uuid.NullUUID{})
	}
	owner := f.product(user, uuid.Nil, "Gazoz", &Size{Value: decimal.RequireFromString("1"), Unit: "L"})
	f.alias(user, owner, "GAZOZ", bim)
	f.alias(user, owner, "GZZ 1L", uuid.NullUUID{})
	f.code(user, owner, gtinOther, CodeGTIN, uuid.NullUUID{})
	nameless := f.product(user, uuid.Nil, "Maden Suyu", nil)
	f.code(user, nameless, gtinOther, CodeStore, bim)

	got, _, err := NewSQLSource(f.pool).Candidates(t.Context(), user, "kola", gtinOther)
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	byID := make(map[uuid.UUID]Candidate)
	for _, c := range got {
		byID[c.ProductID] = c
	}
	gotOwner, ok := byID[owner]
	if !ok {
		t.Fatalf("Candidates() misses the code's owner")
	}
	wantAliases := []Alias{{Name: "gazoz", StoreID: bim}, {Name: "gzz 1000 ml"}}
	if !sameAliases(gotOwner.Aliases, wantAliases) {
		t.Errorf("owner aliases = %+v, want %+v", gotOwner.Aliases, wantAliases)
	}
	if len(gotOwner.Codes) != 1 || gotOwner.Codes[0] != (Code{Value: gtinOther, Kind: CodeGTIN}) {
		t.Errorf("owner codes = %+v, want the GTIN", gotOwner.Codes)
	}
	if gotOwner.Size == nil || gotOwner.Size.String() != "1 l" {
		t.Errorf("owner size = %v, want 1 l", gotOwner.Size)
	}
	gotNameless, ok := byID[nameless]
	if !ok || !sameAliases(gotNameless.Aliases, []Alias{{Name: "maden suyu"}}) {
		t.Errorf("product without aliases = %+v, want it known by its canonical name", gotNameless)
	}
	if len(gotNameless.Codes) != 1 || gotNameless.Codes[0].StoreID != bim {
		t.Errorf("store code = %+v, want it with its store", gotNameless.Codes)
	}
}

func TestSQLSource_ReturnsRejectionsOfTheKeyOnce(t *testing.T) {
	f := newFixture(t)
	user := f.user("alice")
	// Found by name and by code: the rejection must still appear once.
	milk := f.product(user, uuid.Nil, "Sut", nil)
	f.alias(user, milk, "SUT 1 L", uuid.NullUUID{})
	f.code(user, milk, gtinMilk, CodeGTIN, uuid.NullUUID{})
	f.rejection(user, milk, "SUT 1L")
	f.rejection(user, milk, "SUT 2L")
	// Found only by code.
	ayran := f.product(user, uuid.Nil, "Ayran", nil)
	f.code(user, ayran, gtinMilk, CodeGTIN, uuid.NullUUID{})
	f.rejection(user, ayran, "SUT 1L")
	// Rejected but not a candidate at all: nothing to report.
	other := f.product(user, uuid.Nil, "Deterjan", nil)
	f.rejection(user, other, "SUT 1L")

	_, got, err := NewSQLSource(f.pool).Candidates(t.Context(), user, "sut 1000 ml", gtinMilk)
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	want := []Rejection{{ProductID: milk, Name: "sut 1000 ml"}, {ProductID: ayran, Name: "sut 1000 ml"}}
	if !slices.Equal(got, want) {
		t.Errorf("rejections = %+v, want %+v", got, want)
	}
}

func TestSQLSource_ReturnsQueryErrors(t *testing.T) {
	f := newFixture(t)
	user := f.user("alice")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	source := NewSQLSource(f.pool)
	// The name lookup runs only for a key, the code lookup only for a code.
	for _, tc := range []struct{ key, code string }{{"sut", ""}, {"", gtinMilk}} {
		if _, _, err := source.Candidates(ctx, user, tc.key, tc.code); !errors.Is(err, context.Canceled) {
			t.Errorf("Candidates(key %q, code %q) error = %v, want context.Canceled", tc.key, tc.code, err)
		}
	}
}

func TestRecordedSize(t *testing.T) {
	unit := func(s string) *string { return &s }
	one := decimal.NullDecimal{Decimal: decimal.RequireFromString("1.5"), Valid: true}
	tests := []struct {
		name  string
		value decimal.NullDecimal
		unit  *string
		want  string
	}{
		{"canonical unit", one, unit("kg"), "1.5 kg"},
		{"printed spelling", one, unit(" LT "), "1.5 l"},
		{"unknown unit", one, unit("paket"), ""},
		{"no size", decimal.NullDecimal{}, nil, ""},
	}
	for _, tt := range tests {
		if got := sizeString(recordedSize(tt.value, tt.unit)); got != tt.want {
			t.Errorf("%s: recordedSize() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// unrelatedLitres are names of other products that are also sold in one
// litre. Some share their first letters with "sut" on purpose.
var unrelatedLitres = []string{
	"SU", "SUYU", "SODA", "SALGAM", "SIRKE", "SAMPUAN", "SABUN", "SURUP", "SOS",
	"KOLA", "GAZOZ", "AYRAN", "KEFIR", "BOZA", "LIMONATA", "ICE TEA", "MEYVE SUYU",
	"PORTAKAL SUYU", "ELMA SUYU", "VISNE SUYU", "NAR EKSISI", "ZEYTINYAGI",
	"AYCICEK YAGI", "MISIR YAGI", "DETERJAN", "YUMUSATICI", "CAMASIR SUYU",
	"BULASIK DETERJANI", "KOLONYA", "MADEN SUYU", "TONIK", "ENERJI ICECEGI",
}

func TestSQLSource_SameSizeAliasesDoNotCrowdOutTheProduct(t *testing.T) {
	f := newFixture(t)
	user := f.user("alice")
	stores := []uuid.NullUUID{f.store(user, "migros"), f.store(user, "bim"), f.store(user, "a101")}
	// Every unrelated product in one litre, at three stores and in two
	// spellings: 192 aliases that share the line's size.
	for _, name := range unrelatedLitres {
		p := f.product(user, uuid.Nil, name, nil)
		for _, store := range stores {
			f.alias(user, p, name+" 1 L", store)
			f.alias(user, p, name+" PET 1000ML", store)
		}
	}
	milk := f.product(user, uuid.Nil, "Pinar Sut 1 L", nil)
	f.alias(user, milk, "PINAR SUT 1 L", stores[0])
	halfFat := f.product(user, uuid.Nil, "Yarim Yagli Sut 1 L", nil)
	f.alias(user, halfFat, "YARIM YAGLI SUT 1 L", stores[0])

	source := NewSQLSource(f.pool)
	for _, tc := range []struct {
		line string
		want uuid.UUID
	}{
		{"PINAR SUT 1L", milk},
		{"PNR SUT 1L", milk},
		{"P.SUT 1LT", milk},
		{"YARIM YAGLI SUT 1L", halfFat},
		{"YRM YGL SUT 1L", halfFat},
	} {
		got, _, err := source.Candidates(t.Context(), user, Normalize(tc.line).Key, "")
		if err != nil {
			t.Fatalf("Candidates(%q) error = %v", tc.line, err)
		}
		// Candidates come in the order the lookup found them, most similar
		// first.
		rank := slices.IndexFunc(got, func(c Candidate) bool { return c.ProductID == tc.want })
		if rank < 0 {
			t.Errorf("Candidates(%q) misses the right product among %d unrelated ones", tc.line, len(got))
			continue
		}
		t.Logf("%-20q right product is candidate %d of %d", tc.line, rank+1, len(got))
	}
}

func TestMatchLine_GoldenCasesThroughPostgres(t *testing.T) {
	f := newFixture(t)
	matcher := New(NewSQLSource(f.pool))
	var caseNumber uint64
	for _, gc := range loadGolden(t) {
		caseNumber++
		t.Run(gc.Name, func(t *testing.T) {
			user := f.user(fmt.Sprintf("golden-%d", caseNumber))
			stores := make(map[string]uuid.NullUUID)
			store := func(name string) uuid.NullUUID {
				if name == "" {
					return uuid.NullUUID{}
				}
				if _, ok := stores[name]; !ok {
					stores[name] = f.store(user, name)
				}
				return stores[name]
			}
			// The ids keep the alphabetical order of the symbolic names, as
			// in TestGolden, and carry the case number to stay unique.
			ids := goldenProductIDs(gc)
			names := make(map[uuid.UUID]string)
			for _, p := range gc.Products {
				id := ids[p.ID]
				binary.BigEndian.PutUint64(id[:8], caseNumber)
				ids[p.ID] = id
				names[id] = p.ID
				var size *Size
				if p.Size != "" {
					value, unit, _ := strings.Cut(p.Size, " ")
					size = &Size{Value: decimal.RequireFromString(value), Unit: unit}
				}
				f.product(user, id, p.ID, size)
				for _, a := range p.Aliases {
					f.alias(user, id, a.Name, store(a.Store))
				}
				for _, c := range p.Codes {
					f.code(user, id, c.Code, CodeKind(c.Kind), store(c.Store))
				}
			}
			for _, r := range gc.Rejections {
				f.rejection(user, ids[r.Product], r.Name)
			}
			line := Line{Name: gc.Line.Name, Code: gc.Line.Code, StoreID: store(gc.Line.Store)}

			got, err := matcher.MatchLine(t.Context(), user, line)
			if err != nil {
				t.Fatalf("MatchLine() error = %v", err)
			}
			gotCandidates := make([]string, 0, len(got.Candidates))
			for _, c := range got.Candidates {
				gotCandidates = append(gotCandidates, names[c.ProductID])
			}
			if got.Outcome != gc.Want.Outcome || got.Rule != gc.Want.Rule || got.Reason != gc.Want.Reason || names[got.ProductID] != gc.Want.Product {
				t.Errorf("decision = %s rule %d %s %q, want %s rule %d %s %q",
					got.Outcome, got.Rule, got.Reason, names[got.ProductID],
					gc.Want.Outcome, gc.Want.Rule, gc.Want.Reason, gc.Want.Product)
			}
			// A name without letters or numbers has nothing to look up by
			// similarity, so the source finds no aliases for it, while
			// TestGolden hands Decide every product. The decision is the
			// same; only the zero-score candidates differ.
			if Normalize(line.Name).Key != "" && !slices.Equal(gotCandidates, gc.Want.Candidates) {
				t.Errorf("candidates = %v, want %v", gotCandidates, gc.Want.Candidates)
			}
		})
	}
}

// benchmarkWords build synthetic product names.
var (
	benchmarkBrands   = []string{"PINAR", "ICIM", "SEK", "TORKU", "ULKER", "ETI", "TAT", "TAMEK", "BIZIM", "KOSKA"}
	benchmarkProducts = []string{
		"SUT", "AYRAN", "YOGURT", "PEYNIR", "KASAR", "TEREYAG", "KREMA", "LABNE", "KEFIR", "PUDING",
		"BISKUVI", "GOFRET", "KRAKER", "CIKOLATA", "KEK", "SALCA", "KETCAP", "MAYONEZ", "TURSU", "RECEL",
		"BAL", "HELVA", "MAKARNA", "PIRINC", "BULGUR", "MERCIMEK", "NOHUT", "FASULYE", "UN", "SEKER",
		"TUZ", "CAY", "KAHVE", "SU", "KOLA", "GAZOZ", "MEYVE SUYU", "ZEYTIN", "ZEYTINYAGI", "SIRKE",
		"DETERJAN", "SABUN", "SAMPUAN", "DIS MACUNU", "PECETE", "HAVLU", "SUCUK", "SALAM", "SOSIS", "YUMURTA",
	}
	benchmarkSizes = []string{"200 ML", "500 G", "1 L", "1 KG", "750 G"}
)

// BenchmarkMatchLine matches lines for a user with 5,000 aliases.
func BenchmarkMatchLine(b *testing.B) {
	f := newFixture(b)
	user := f.user("alice")
	other := f.user("bob")
	seedAliases(b, f, other, 5000) // another user's rows must not slow the lookup
	seedAliases(b, f, user, 5000)
	f.exec(`ANALYZE`)

	matcher := New(NewSQLSource(f.pool))
	lines := []Line{
		{Name: "PNR SUT 1L"},
		{Name: "ICIM AYRAN 200ML"},
		{Name: "ULKER CKL GOFRET 500G"},
		{Name: "TAT SALCA 1KG", Code: gtinMilk},
		{Name: "YENI URUN"},
	}
	b.ResetTimer()
	i := 0
	for b.Loop() {
		if _, err := matcher.MatchLine(b.Context(), user, lines[i%len(lines)]); err != nil {
			b.Fatalf("MatchLine() error = %v", err)
		}
		i++
	}
}

// seedAliases gives user n aliases: one product per brand, product and
// size, each with a full and an abbreviated alias, and a barcode on one.
func seedAliases(b *testing.B, f fixture, user uuid.UUID, n int) {
	b.Helper()
	combinations := len(benchmarkBrands) * len(benchmarkProducts) * len(benchmarkSizes)
	if n > 2*combinations {
		b.Fatalf("seedAliases can make at most %d distinct aliases", 2*combinations)
	}
	var productIDs, aliasProducts []uuid.UUID
	var productNames, rawNames, keys []string
	for i := 0; len(rawNames) < n; i++ {
		brand := benchmarkBrands[i%len(benchmarkBrands)]
		name := benchmarkProducts[(i/len(benchmarkBrands))%len(benchmarkProducts)]
		size := benchmarkSizes[i/(len(benchmarkBrands)*len(benchmarkProducts))]
		full := fmt.Sprintf("%s %s %s", brand, name, size)
		short := fmt.Sprintf("%s. %s %s", brand[:2], name, strings.ReplaceAll(size, " ", ""))
		id := uuid.New()
		productIDs = append(productIDs, id)
		productNames = append(productNames, full)
		for _, raw := range []string{full, short} {
			aliasProducts = append(aliasProducts, id)
			rawNames = append(rawNames, raw)
			keys = append(keys, Normalize(raw).Key)
		}
	}
	f.exec(`INSERT INTO products (id, user_id, canonical_name) SELECT unnest($1::uuid[]), $2, unnest($3::text[])`,
		productIDs, user, productNames)
	f.exec(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name)
		SELECT $1, unnest($2::uuid[]), unnest($3::text[]), unnest($4::text[])`,
		user, aliasProducts, rawNames, keys)
	f.exec(`INSERT INTO product_codes (user_id, product_id, code, kind) VALUES ($1, $2, $3, 'gtin')`,
		user, productIDs[0], gtinMilk)
}

func sameAliases(got, want []Alias) bool {
	sortAliases := func(a []Alias) []Alias {
		s := slices.Clone(a)
		slices.SortFunc(s, func(x, y Alias) int { return strings.Compare(x.Name, y.Name) })
		return s
	}
	return slices.Equal(sortAliases(got), sortAliases(want))
}
