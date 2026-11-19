package suggestions

import (
	"fmt"
	"strings"
)

// promptVersion marks the fixed prompt template. Bump it whenever the
// template changes, so a stored suggestion tells which prompt produced
// it.
const promptVersion = 1

// systemPrompt is the fixed system message of prompt version 1. It fixes
// the task, the answer shape and the rules; the user message carries the
// data.
const systemPrompt = "You clean up receipt line items for a personal expense tracker. " +
	"Answer with one JSON object of exactly this shape: " +
	`{"lines":[{"position":<line number>,"name":"<clean product name>","category":"<one of the listed categories>"}]}. ` +
	"Rules: name is the product alone, in the language of the receipt, corrected and shortened to what a person would call the product; " +
	"never include a store name, a size, a quantity or a price, and never invent a product the line does not name. " +
	"category is exactly one of the listed category names, or null when none fits; never invent a category. " +
	"Answer for every numbered line, with the JSON object only."

// buildMessages renders the fixed template with one request's data: the
// store name when present, the user's category names and the numbered raw
// line texts, and nothing else.
func buildMessages(req Request) []Message {
	var user strings.Builder
	if name := singleLine(req.StoreName); name != "" {
		user.WriteString("Store: ")
		user.WriteString(name)
		user.WriteByte('\n')
	}
	names := make([]string, 0, len(req.Categories))
	for _, category := range req.Categories {
		if name := singleLine(category); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		user.WriteString("Categories: none")
	} else {
		user.WriteString("Categories: ")
		user.WriteString(strings.Join(names, ", "))
	}
	user.WriteByte('\n')
	for _, line := range req.Lines {
		fmt.Fprintf(&user, "%d. %s\n", line.Position, singleLine(line.RawName))
	}
	return []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: strings.TrimSuffix(user.String(), "\n")},
	}
}

// singleLine collapses whitespace so one receipt field cannot break the
// numbered, one-line-per-item format of the user message.
func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
