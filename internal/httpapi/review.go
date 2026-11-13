package httpapi

import "context"

// reviewHandlers serves the operations of the review tag: review flags.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type reviewHandlers struct{}

// ListReviewFlags returns one page of open flags, oldest receipt first.
func (reviewHandlers) ListReviewFlags(_ context.Context, _ ListReviewFlagsRequestObject) (ListReviewFlagsResponseObject, error) {
	return nil, notImplemented()
}

// ResolveReviewFlag closes a flag with one of the resolutions its kind allows.
func (reviewHandlers) ResolveReviewFlag(_ context.Context, _ ResolveReviewFlagRequestObject) (ResolveReviewFlagResponseObject, error) {
	return nil, notImplemented()
}
