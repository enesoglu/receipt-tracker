// Package suggestions asks an optional, self-hosted language model for a
// clean product name and a category for the receipt lines the matcher
// could not confidently resolve (ARCHITECTURE §5.8).
//
// Matching stays deterministic: the model only suggests, the user accepts
// or ignores each suggestion in review, and accepting runs the existing
// resolution paths. Nothing on a user request path calls the model; a
// background job generates suggestions through the [Suggester] interface,
// and the model never writes products, aliases or line items.
//
// The conversation is the fixed, versioned prompt template of this
// package, spoken to a chat endpoint behind the [Completer] interface.
// The concrete client for the two supported wire protocols lives in
// subpackage llm; tests use hand-written fakes, never a real model.
package suggestions

import (
	"context"
	"fmt"
)

// Message is one chat message of the fixed conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Line is one raw receipt line to suggest for.
type Line struct {
	// Position is the line's number in the request, 1-based; the answer
	// references its lines by this number.
	Position int
	// RawName is the printed line text as extracted.
	RawName string
}

// Request is one batch of raw receipt lines.
type Request struct {
	// StoreName is the receipt's store name when matching found one.
	StoreName string
	// Categories are the user's own category names, the only categories
	// the model may pick from.
	Categories []string
	// Lines are the raw lines, in receipt order.
	Lines []Line
}

// Suggestion is the model's validated answer for one requested line.
type Suggestion struct {
	// Position identifies the requested line.
	Position int
	// Name is the suggested clean product name, 1 to 200 characters.
	Name string
	// Category is the requested category the model picked, spelled as
	// the user spelled it; empty when the model picked none.
	Category string
}

// Suggester suggests clean names and categories for raw receipt lines. It
// is consumed by the background job; implementations never touch the
// database.
type Suggester interface {
	// Suggest returns the validated suggestions for req, in answer
	// order. A failure is reported as an [*Error]; a line the model did
	// not answer, or answered in an unusable form, is simply absent from
	// the result.
	Suggest(ctx context.Context, req Request) ([]Suggestion, error)
}

// Completer is one chat completion against the suggestion endpoint. It is
// implemented by the protocol client in subpackage llm and by fakes in
// tests; a failure is reported as an [*Error].
type Completer interface {
	// Complete sends the conversation and returns the model's answer as
	// text.
	Complete(ctx context.Context, messages []Message) (string, error)
}

// ErrorKind says what the job should do after a failed suggestion call.
type ErrorKind int

// The kinds of failure.
const (
	// Busy means the endpoint answered that the shared model is in use
	// (HTTP 429). The job should run again later without counting the
	// attempt.
	Busy ErrorKind = iota
	// Unreachable means the endpoint could not be reached or erred:
	// connection refused, timeout or a 5xx answer. The job should retry
	// with backoff, which records the failure where the user sees it.
	Unreachable
	// Refused means the endpoint rejected the call with any other 4xx,
	// or its answer was unusable. Retrying the same call cannot help.
	Refused
)

// Error is a failed suggestion call, classified from the HTTP status and
// the answer's shape only: request and response bodies carry receipt line
// text and must never appear in an error or a log.
type Error struct {
	// Kind classifies the failure.
	Kind ErrorKind
	// Status is the HTTP status when there was one, zero otherwise.
	Status int
	cause  error
}

// NewError returns a suggestion failure and its classification. The cause
// must not carry a request or response body.
func NewError(kind ErrorKind, status int, cause error) *Error {
	return &Error{Kind: kind, Status: status, cause: cause}
}

// Error describes the failure without any receipt text.
func (e *Error) Error() string {
	var message string
	switch e.Kind {
	case Busy:
		message = "model busy"
	case Unreachable:
		message = "suggestion endpoint unreachable"
	case Refused:
		message = "suggestion endpoint refused the call"
	}
	if e.Status != 0 {
		message = fmt.Sprintf("%s (http %d)", message, e.Status)
	}
	if e.cause != nil {
		message += ": " + e.cause.Error()
	}
	return "suggest lines: " + message
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.cause }

// Engine is the [Suggester] that speaks the fixed prompt template to a
// [Completer] and validates the answer.
type Engine struct {
	complete Completer
}

// NewSuggester returns the [Suggester] for one chat endpoint.
func NewSuggester(complete Completer) *Engine {
	return &Engine{complete: complete}
}

// Suggest implements [Suggester].
func (e *Engine) Suggest(ctx context.Context, req Request) ([]Suggestion, error) {
	if len(req.Lines) == 0 {
		return nil, nil
	}
	content, err := e.complete.Complete(ctx, buildMessages(req))
	if err != nil {
		return nil, err
	}
	return parseAnswer(content, req)
}
