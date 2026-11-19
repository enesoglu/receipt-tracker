package llm

import (
	"encoding/json"
	"errors"

	"github.com/enesoglu/receipt-tracker/internal/suggestions"
)

// openaiWire speaks the OpenAI-compatible chat completions protocol.
type openaiWire struct{}

// path implements [wire].
func (openaiWire) path() string { return "/chat/completions" }

// openaiResponseFormat asks for a JSON object answer.
type openaiResponseFormat struct {
	Type string `json:"type"`
}

// openaiRequest is the body of a chat completions call.
type openaiRequest struct {
	Model          string                `json:"model"`
	Messages       []suggestions.Message `json:"messages"`
	Stream         bool                  `json:"stream"`
	ResponseFormat openaiResponseFormat  `json:"response_format"`
}

// body implements [wire].
func (openaiWire) body(model string, messages []suggestions.Message) ([]byte, error) {
	return json.Marshal(openaiRequest{
		Model:          model,
		Messages:       messages,
		Stream:         false,
		ResponseFormat: openaiResponseFormat{Type: "json_object"},
	})
}

// openaiAnswer is the part of a chat completions answer that is read.
type openaiAnswer struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// content implements [wire].
func (openaiWire) content(data []byte) (string, error) {
	var answer openaiAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return "", err
	}
	if len(answer.Choices) == 0 || answer.Choices[0].Message.Content == "" {
		return "", errors.New("no message content")
	}
	return answer.Choices[0].Message.Content, nil
}
