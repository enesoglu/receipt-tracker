package suggestions_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/suggestions"
)

// stubCompleter records the conversation and plays back a canned answer.
type stubCompleter struct {
	messages []suggestions.Message
	answer   string
	err      error
	calls    int
}

// Complete records the conversation and answers.
func (s *stubCompleter) Complete(_ context.Context, messages []suggestions.Message) (string, error) {
	s.calls++
	s.messages = messages
	if s.err != nil {
		return "", s.err
	}
	return s.answer, nil
}

// requestedLines builds a request whose user message is fully covered by
// the assertions below: a store, three categories and three numbered
// lines, one with doubled spaces and one with a line break.
func requestedLines() suggestions.Request {
	return suggestions.Request{
		StoreName:  "MIGROS",
		Categories: []string{"Groceries", "Drinks", "Household"},
		Lines: []suggestions.Line{
			{Position: 1, RawName: "AYRN 1LT"},
			{Position: 2, RawName: "EKMEK  500G"},
			{Position: 3, RawName: "UNLU\nCAY"},
		},
	}
}

func TestSuggest_UserMessageCarriesTheDataAndNothingElse(t *testing.T) {
	stub := &stubCompleter{answer: `{"lines":[{"position":1,"name":"Ayran","category":"Drinks"}]}`}
	engine := suggestions.NewSuggester(stub)
	if _, err := engine.Suggest(context.Background(), requestedLines()); err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}

	if len(stub.messages) != 2 {
		t.Fatalf("conversation has %d messages, want a system and a user message", len(stub.messages))
	}
	system, user := stub.messages[0], stub.messages[1]

	if system.Role != "system" {
		t.Errorf("system message role = %q, want %q", system.Role, "system")
	}
	if !strings.Contains(system.Content, `"lines":[{"position"`) || !strings.Contains(system.Content, "null") {
		t.Errorf("system message does not fix the answer shape and the null category:\n%s", system.Content)
	}

	if user.Role != "user" {
		t.Errorf("user message role = %q, want %q", user.Role, "user")
	}
	want := "Store: MIGROS\n" +
		"Categories: Groceries, Drinks, Household\n" +
		"1. AYRN 1LT\n" +
		"2. EKMEK 500G\n" +
		"3. UNLU CAY"
	if user.Content != want {
		t.Errorf("user message mismatch (-want +got):\n%s", cmp.Diff(want, user.Content))
	}
}

func TestSuggest_UserMessageWithoutStoreOrCategories(t *testing.T) {
	stub := &stubCompleter{answer: `{"lines":[{"position":1,"name":"Ayran","category":null}]}`}
	engine := suggestions.NewSuggester(stub)
	req := suggestions.Request{Lines: []suggestions.Line{{Position: 1, RawName: "AYRN 1LT"}}}
	if _, err := engine.Suggest(context.Background(), req); err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	user := stub.messages[1]
	want := "Categories: none\n1. AYRN 1LT"
	if user.Content != want {
		t.Errorf("user message mismatch (-want +got):\n%s", cmp.Diff(want, user.Content))
	}
	if strings.Contains(user.Content, "Store:") {
		t.Error("user message names a store the request does not have")
	}
}

func TestSuggest_ValidatesTheAnswer(t *testing.T) {
	answer := `{"lines":[` +
		`{"position":2,"name":"  Ekmek ","category":"GROCERIES"},` +
		`{"position":1,"name":"Ayran","category":"Drinks"},` +
		`{"position":3,"name":"Unlu cay","category":null}` +
		`]}`
	stub := &stubCompleter{answer: answer}
	engine := suggestions.NewSuggester(stub)
	got, err := engine.Suggest(context.Background(), requestedLines())
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	// The answer's order is kept; a category comes back in the user's
	// own spelling, whatever case the model used.
	want := []suggestions.Suggestion{
		{Position: 2, Name: "Ekmek", Category: "Groceries"},
		{Position: 1, Name: "Ayran", Category: "Drinks"},
		{Position: 3, Name: "Unlu cay", Category: ""},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("suggestions mismatch (-want +got):\n%s", diff)
	}
}

func TestSuggest_DropsUnusableItemsAndKeepsTheGoodOnes(t *testing.T) {
	longName := strings.Repeat("x", 201)
	answer := `{"lines":[` +
		`{"position":1,"name":"Ayran","category":"Drinks"},` +
		`{"position":9,"name":"No such line","category":"Drinks"},` +
		`{"position":1,"name":"Duplicate position","category":"Drinks"},` +
		`{"position":2,"name":"   ","category":"Drinks"},` +
		`{"position":3,"name":"` + longName + `","category":"Drinks"},` +
		`{"position":"4","name":"Position as text","category":"Drinks"},` +
		`{"name":"No position","category":"Drinks"},` +
		`{"position":2,"name":"Ekmek","category":"Not a user category"},` +
		`{"position":3,"name":"Cay","category":"HOUSHOLD junk"}` +
		`]}`
	stub := &stubCompleter{answer: answer}
	engine := suggestions.NewSuggester(stub)
	got, err := engine.Suggest(context.Background(), requestedLines())
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	want := []suggestions.Suggestion{
		{Position: 1, Name: "Ayran", Category: "Drinks"},
		{Position: 2, Name: "Ekmek", Category: ""},
		{Position: 3, Name: "Cay", Category: ""},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("suggestions mismatch (-want +got):\n%s", diff)
	}
}

func TestSuggest_RejectsAnswersWithoutOneUsableItem(t *testing.T) {
	tests := []struct {
		name   string
		answer string
	}{
		{name: "not json", answer: `here is what I found: nothing`},
		{name: "lines of the wrong type", answer: `{"lines":"all of them"}`},
		{name: "empty lines", answer: `{"lines":[]}`},
		{name: "only unusable lines", answer: `{"lines":[{"position":1,"name":"  ","category":null}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := suggestions.NewSuggester(&stubCompleter{answer: test.answer})
			got, err := engine.Suggest(context.Background(), requestedLines())
			if got != nil {
				t.Fatalf("Suggest() = %v, want no suggestions", got)
			}
			var failure *suggestions.Error
			if !errors.As(err, &failure) || failure.Kind != suggestions.Refused {
				t.Fatalf("Suggest() error = %v, want a refused %T", err, failure)
			}
			if strings.Contains(err.Error(), "AYRN") {
				t.Error("the error quotes the answer, which carries receipt text")
			}
		})
	}
}

func TestSuggest_PassesEndpointFailuresThrough(t *testing.T) {
	cause := errors.New("synthetic endpoint failure")
	engine := suggestions.NewSuggester(&stubCompleter{err: cause})
	got, err := engine.Suggest(context.Background(), requestedLines())
	if got != nil {
		t.Fatalf("Suggest() = %v, want no suggestions", got)
	}
	if !errors.Is(err, cause) {
		t.Errorf("Suggest() error = %v, want the endpoint's %v passed through", err, cause)
	}
}

func TestSuggest_AskingForNoLinesCallsNoEndpoint(t *testing.T) {
	stub := &stubCompleter{}
	engine := suggestions.NewSuggester(stub)
	got, err := engine.Suggest(context.Background(), suggestions.Request{})
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if got != nil {
		t.Errorf("Suggest() = %v, want none", got)
	}
	if stub.calls != 0 {
		t.Errorf("endpoint called %d times, want none", stub.calls)
	}
}

func TestError_DescribesTheFailureWithoutStatusDetails(t *testing.T) {
	tests := []struct {
		name string
		err  *suggestions.Error
		want string
	}{
		{
			name: "busy",
			err:  suggestions.NewError(suggestions.Busy, 429, nil),
			want: "suggest lines: model busy (http 429)",
		},
		{
			name: "unreachable",
			err:  suggestions.NewError(suggestions.Unreachable, 0, nil),
			want: "suggest lines: suggestion endpoint unreachable",
		},
		{
			name: "refused",
			err:  suggestions.NewError(suggestions.Refused, 403, nil),
			want: "suggest lines: suggestion endpoint refused the call (http 403)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.err.Error() != test.want {
				t.Errorf("Error() = %q, want %q", test.err.Error(), test.want)
			}
		})
	}
}
