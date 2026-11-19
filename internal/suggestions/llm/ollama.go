package llm

import (
	"encoding/json"
	"errors"

	"github.com/enesoglu/receipt-tracker/internal/suggestions"
)

// ollamaWire speaks Ollama's native API.
type ollamaWire struct{}

// path implements [wire].
func (ollamaWire) path() string { return "/api/chat" }

// ollamaOptions bounds the context window and the answer length.
type ollamaOptions struct {
	NumCtx     int `json:"num_ctx"`
	NumPredict int `json:"num_predict"`
}

// ollamaRequest is the body of an /api/chat call.
type ollamaRequest struct {
	Model    string                `json:"model"`
	Messages []suggestions.Message `json:"messages"`
	Stream   bool                  `json:"stream"`
	Format   string                `json:"format"`
	Options  ollamaOptions         `json:"options"`
}

// body implements [wire].
func (ollamaWire) body(model string, messages []suggestions.Message) ([]byte, error) {
	return json.Marshal(ollamaRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
		Format:   "json",
		Options:  ollamaOptions{NumCtx: 4096, NumPredict: 512},
	})
}

// ollamaAnswer is the part of an /api/chat answer that is read.
type ollamaAnswer struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// content implements [wire].
func (ollamaWire) content(data []byte) (string, error) {
	var answer ollamaAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return "", err
	}
	if answer.Message.Content == "" {
		return "", errors.New("no message content")
	}
	return answer.Message.Content, nil
}
