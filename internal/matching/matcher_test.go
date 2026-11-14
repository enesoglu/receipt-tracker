package matching

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeSource is a hand-written CandidateSource that records what it was
// asked for.
type fakeSource struct {
	candidates []Candidate
	rejections []Rejection
	err        error

	gotUser uuid.UUID
	gotKey  string
	gotCode string
}

func (f *fakeSource) Candidates(_ context.Context, userID uuid.UUID, key, code string) ([]Candidate, []Rejection, error) {
	f.gotUser, f.gotKey, f.gotCode = userID, key, code
	return f.candidates, f.rejections, f.err
}

func TestMatchLine_LoadsByUserAndKeyThenDecides(t *testing.T) {
	user := uuid.NewSHA1(uuid.NameSpaceOID, []byte("user"))
	source := &fakeSource{
		candidates: []Candidate{known("milk", "PINAR SUT 1 LT").withGTIN(gtinMilk)},
	}
	line := Line{Name: "Pınar Süt 1LT", Code: gtinMilk, StoreID: storeID("bim")}

	got, err := New(source).MatchLine(t.Context(), user, line)
	if err != nil {
		t.Fatalf("MatchLine() error = %v", err)
	}
	if source.gotUser != user || source.gotKey != "pinar sut 1000 ml" || source.gotCode != gtinMilk {
		t.Errorf("source asked for user %v key %q code %q, want %v %q %q",
			source.gotUser, source.gotKey, source.gotCode, user, "pinar sut 1000 ml", gtinMilk)
	}
	checkDecision(t, got, want{AutoLink, 6, ReasonCodeMatch, "milk", []string{"milk"}})
}

func TestMatchLine_AppliesRejections(t *testing.T) {
	source := &fakeSource{
		candidates: []Candidate{known("milk", "PINAR SUT 1 LT")},
		rejections: []Rejection{{ProductID: productID("milk"), Name: "pinar sut 1000 ml"}},
	}
	got, err := New(source).MatchLine(t.Context(), uuid.New(), Line{Name: "PINAR SUT 1L"})
	if err != nil {
		t.Fatalf("MatchLine() error = %v", err)
	}
	checkDecision(t, got, want{CreateNew, 10, ReasonNoMatch, "", []string{}})
}

func TestMatchLine_ReturnsSourceErrors(t *testing.T) {
	failure := errors.New("database is down")
	_, err := New(&fakeSource{err: failure}).MatchLine(t.Context(), uuid.New(), Line{Name: "SUT"})
	if !errors.Is(err, failure) {
		t.Errorf("MatchLine() error = %v, want it to wrap %v", err, failure)
	}
}
