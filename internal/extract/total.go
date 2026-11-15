package extract

import "github.com/shopspring/decimal"

// CompleteTotal fills a missing receipt total when two independent figures
// agree on it: the sum of the line totals, and the subtotal plus the tax.
// Some receipts print the payable amount under a label the provider does
// not read as the total, while it still reads a pre-tax subtotal and the
// tax. Without a total every such receipt would be flagged as missing one.
//
// Nothing is filled unless every line has a total and both sums are equal
// to the cent. A total the provider found is never changed, even when it
// disagrees with the lines; the review rules flag that. The filled total
// carries the lower confidence of the subtotal and the tax.
func CompleteTotal(r Result) Result {
	if r.Total.Found || !r.Subtotal.Found || !r.Tax.Found || len(r.Lines) == 0 {
		return r
	}
	lines := decimal.Zero
	for _, line := range r.Lines {
		if !line.Total.Found {
			return r
		}
		lines = lines.Add(line.Total.Value)
	}
	expected := r.Subtotal.Value.Add(r.Tax.Value)
	if !lines.Round(2).Equal(expected.Round(2)) {
		return r
	}
	r.Total = Field[decimal.Decimal]{
		Value:      expected,
		Confidence: min(r.Subtotal.Confidence, r.Tax.Confidence),
		Found:      true,
	}
	return r
}
