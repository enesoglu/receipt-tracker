package suggestions

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// maxNameLength is the longest suggested name the schema accepts.
const maxNameLength = 200

// answer is the top level of the model's answer. The lines are raw JSON
// because one malformed item must not discard the good ones.
type answer struct {
	Lines []json.RawMessage `json:"lines"`
}

// answerLine is one item of the answer's lines array. An item that does
// not decode into this shape is dropped, not fatal.
type answerLine struct {
	Position *int    `json:"position"`
	Name     *string `json:"name"`
	Category *string `json:"category"`
}

// parseAnswer validates the model's answer for req. An item is kept only
// when its position is one of the requested and not yet answered, and its
// name is 1 to 200 characters after trimming; a category is kept only when
// it equals one of the request's category names case-insensitively, in
// the user's own spelling. An answer that is not valid JSON, or that
// yields no usable item at all, is refused: retrying it cannot help.
func parseAnswer(content string, req Request) ([]Suggestion, error) {
	var parsed answer
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, NewError(Refused, 0, errors.New("answer is not valid json"))
	}
	requested := make(map[int]bool, len(req.Lines))
	for _, line := range req.Lines {
		requested[line.Position] = true
	}
	categories := make(map[string]string, len(req.Categories))
	for _, name := range req.Categories {
		name = singleLine(name)
		categories[strings.ToLower(name)] = name
	}
	var result []Suggestion
	seen := make(map[int]bool, len(req.Lines))
	for _, raw := range parsed.Lines {
		var item answerLine
		if err := json.Unmarshal(raw, &item); err != nil || item.Position == nil || item.Name == nil {
			continue
		}
		position := *item.Position
		if !requested[position] || seen[position] {
			continue
		}
		name := strings.TrimSpace(*item.Name)
		if name == "" || utf8.RuneCountInString(name) > maxNameLength {
			continue
		}
		category := ""
		if item.Category != nil {
			category = categories[strings.ToLower(strings.TrimSpace(*item.Category))]
		}
		seen[position] = true
		result = append(result, Suggestion{Position: position, Name: name, Category: category})
	}
	if len(result) == 0 {
		return nil, NewError(Refused, 0, errors.New("answer has no usable line"))
	}
	return result, nil
}
