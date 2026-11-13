package httpapi

import "context"

// authHandlers serves the operations of the auth tag: the signed-in user and sign-out.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type authHandlers struct{}

// GetMe returns the user who owns the session.
func (authHandlers) GetMe(_ context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	return nil, notImplemented()
}

// Logout destroys the session.
func (authHandlers) Logout(_ context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	return nil, notImplemented()
}
