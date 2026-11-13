// Package apperr defines the error kinds every package uses to classify a
// failure, so that only the transport layer has to know about status codes.
//
// A domain package returns either a bare kind or an [Error] carrying a kind:
//
//	return apperr.New(apperr.NotFound, "receipt not found")
//	return apperr.Wrap(apperr.Unavailable, "image storage is not reachable", err)
//
// Callers classify with the standard library:
//
//	if errors.Is(err, apperr.NotFound) { ... }
//
// errors.Is follows the whole chain, so an Unavailable error whose cause was
// NotFound matches both. Code that needs exactly one answer, such as the
// mapping to a status code, uses [KindOf], which reports the outermost kind.
//
// The message of an [Error] is written for the person using the application
// and is shown to them. The wrapped cause is for logs only and never leaves
// the server.
package apperr

import (
	"errors"
	"slices"
)

// Kind classifies a failure. A Kind is itself an error, so a bare kind can be
// returned, wrapped with fmt.Errorf and matched with errors.Is.
type Kind uint8

// The kinds are deliberately few: each one changes how a caller must react.
const (
	// NotFound means the resource does not exist for this user. A row owned by
	// another user is reported as NotFound, so its existence is not revealed.
	NotFound Kind = iota + 1
	// Invalid means the input cannot be accepted as it is.
	Invalid
	// Conflict means the input is acceptable but the current state forbids the
	// change, for example a duplicate name.
	Conflict
	// Forbidden means the user is known but may not perform the action.
	Forbidden
	// Unavailable means a dependency cannot serve the request right now and a
	// later retry may succeed.
	Unavailable
)

// Error returns a short lower-case description of the kind.
func (k Kind) Error() string {
	switch k {
	case NotFound:
		return "not found"
	case Invalid:
		return "invalid"
	case Conflict:
		return "conflict"
	case Forbidden:
		return "forbidden"
	case Unavailable:
		return "unavailable"
	default:
		return "unknown error kind"
	}
}

// FieldError describes what is wrong with one input field.
type FieldError struct {
	// Field names the input as the client sent it, for example "lineTotal".
	Field string
	// Message says what is wrong, in words the user can act on.
	Message string
}

// Error is a classified failure with a message that is safe to show the user.
type Error struct {
	// Kind decides how the failure is reported.
	Kind Kind
	// Message is shown to the user. It must not contain secrets, identifiers
	// of other users or internal details.
	Message string
	// Code is an optional stable identifier that clients can branch on, for
	// example "storage_reconnect_needed". It is lower-case snake_case.
	Code string
	// Fields lists input problems one by one. It is used with Invalid.
	Fields []FieldError

	cause error
}

// New returns an error of the given kind with a message for the user.
func New(kind Kind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

// Wrap returns an error of the given kind that keeps cause for logs. The
// cause stays reachable through errors.Is and errors.As but is never shown
// to the user.
func Wrap(kind Kind, message string, cause error) *Error {
	return &Error{Kind: kind, Message: message, cause: cause}
}

// WithCode returns a copy of e that carries a stable machine-readable code.
func (e *Error) WithCode(code string) *Error {
	clone := *e
	clone.Code = code
	return &clone
}

// WithField returns a copy of e that also reports a problem with one field.
func (e *Error) WithField(field, message string) *Error {
	clone := *e
	clone.Fields = append(slices.Clone(e.Fields), FieldError{Field: field, Message: message})
	return &clone
}

// Error returns the message followed by the cause, for logs.
func (e *Error) Error() string {
	message := e.Message
	if message == "" {
		message = e.Kind.Error()
	}
	if e.cause == nil {
		return message
	}
	return message + ": " + e.cause.Error()
}

// Unwrap returns the cause, so errors.Is and errors.As see through e.
func (e *Error) Unwrap() error { return e.cause }

// Is reports whether target is the kind of e, which makes
// errors.Is(err, apperr.NotFound) true for every NotFound error.
func (e *Error) Is(target error) bool {
	kind, ok := target.(Kind)
	return ok && kind == e.Kind
}

// KindOf returns the kind of the outermost classified error in err's chain.
// It reports false for an error that was never classified.
func KindOf(err error) (Kind, bool) {
	var classified *Error
	if errors.As(err, &classified) {
		return classified.Kind, true
	}
	var kind Kind
	if errors.As(err, &kind) {
		return kind, true
	}
	return 0, false
}
