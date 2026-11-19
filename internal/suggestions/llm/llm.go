// Package llm is the client for the self-hosted suggestion model over one
// of two wire protocols (ARCHITECTURE §5.8): OpenAI-compatible chat
// completions and Ollama's native API, selected by [Options.Protocol].
//
// The endpoint is usually a network-level relay to a model that runs on
// the owner's PC: it needs no key by default and its availability is not
// guaranteed, so failures are classified for the job's retry policy.
// Request and response bodies carry receipt line text and never appear in
// an error or a log.
//
// The configured base URL is used verbatim, with only the documented path
// of the protocol appended: the client never sniffs the URL or rewrites
// it for a provider.
package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/suggestions"
)

// maxResponseBytes bounds a response body read into memory. A chat answer
// is a few kilobytes; anything larger is not one.
const maxResponseBytes = 1 << 20

// defaultTimeout bounds one request when no other timeout is set. A
// local model can be slow to answer and even to load.
const defaultTimeout = 5 * time.Minute

// Protocol selects the wire protocol of the endpoint.
type Protocol string

// The wire protocols.
const (
	// OpenAI posts to {base}/chat/completions and reads
	// choices[0].message.content.
	OpenAI Protocol = "openai"
	// Ollama posts to {base}/api/chat and reads message.content.
	Ollama Protocol = "ollama"
)

// Options configures a [Client].
type Options struct {
	// Protocol selects the wire shape. Required.
	Protocol Protocol
	// BaseURL is the endpoint origin with an optional path prefix, used
	// verbatim with only the protocol's documented path appended.
	// Required.
	BaseURL string
	// Model names the model the endpoint serves. Required.
	Model string
	// APIKey is the optional bearer token of a protected endpoint. It
	// is sent in a header and never logged or put into an error.
	APIKey string
	// Timeout bounds one request. Default 5 minutes.
	Timeout time.Duration
	// HTTP is the transport. Default: a client with the timeout. A set
	// client is used as-is.
	HTTP *http.Client
}

// wire is the protocol-dependent half of a call: the path, the request
// body and where the answer text sits.
type wire interface {
	// path is appended to the base URL.
	path() string
	// body renders the request the protocol expects.
	body(model string, messages []suggestions.Message) ([]byte, error)
	// content reads the model's answer text from a response body.
	content(data []byte) (string, error)
}

// Client is the suggestion-model endpoint. It implements
// [suggestions.Completer] and is used by the job worker.
type Client struct {
	opts Options
	base string
	wire wire
}

// New returns a client for the endpoint, speaking the selected protocol.
func New(opts Options) (*Client, error) {
	if opts.Protocol != OpenAI && opts.Protocol != Ollama {
		return nil, errors.New("llm protocol must be openai or ollama")
	}
	if opts.Model == "" {
		return nil, errors.New("llm model is required")
	}
	base, err := url.Parse(strings.TrimRight(opts.BaseURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("llm base url must be an absolute http or https URL")
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("llm base url must not carry credentials, a query or a fragment")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: opts.Timeout}
	}
	var shape wire
	if opts.Protocol == Ollama {
		shape = ollamaWire{}
	} else {
		shape = openaiWire{}
	}
	return &Client{opts: opts, base: strings.TrimRight(opts.BaseURL, "/"), wire: shape}, nil
}

// Complete implements [suggestions.Completer]: it posts the conversation
// once and returns the model's answer text.
func (c *Client) Complete(ctx context.Context, messages []suggestions.Message) (string, error) {
	body, err := c.wire.body(c.opts.Model, messages)
	if err != nil {
		return "", suggestions.NewError(suggestions.Refused, 0, errors.New("request could not be built"))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+c.wire.path(), bytes.NewReader(body))
	if err != nil {
		return "", suggestions.NewError(suggestions.Refused, 0, errors.New("invalid request URL"))
	}
	request.Header.Set("Content-Type", "application/json")
	if c.opts.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	}
	answer, err := c.opts.HTTP.Do(request)
	if err != nil {
		// A *url.Error names the URL, which holds no secret and no
		// receipt text; the key is in a header and the lines are in
		// the body, neither of which is part of the error.
		return "", suggestions.NewError(suggestions.Unreachable, 0, err)
	}
	defer func() { _ = answer.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(answer.Body, maxResponseBytes))
	if err != nil {
		return "", suggestions.NewError(suggestions.Unreachable, answer.StatusCode, errors.New("answer was not read completely"))
	}
	switch status := answer.StatusCode; {
	case status == http.StatusOK:
	case status == http.StatusTooManyRequests:
		return "", suggestions.NewError(suggestions.Busy, status, errors.New("model busy"))
	case status >= 500:
		return "", suggestions.NewError(suggestions.Unreachable, status, fmt.Errorf("http %d", status))
	default:
		return "", suggestions.NewError(suggestions.Refused, status, fmt.Errorf("http %d", status))
	}
	content, err := c.wire.content(data)
	if err != nil {
		return "", suggestions.NewError(suggestions.Refused, http.StatusOK, errors.New("answer has no message content"))
	}
	return content, nil
}
