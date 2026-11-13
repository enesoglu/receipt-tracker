package httpapi

import "context"

// settingsHandlers serves the operations of the settings tag: per-user settings and the extraction allowance.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type settingsHandlers struct{}

// GetSettings returns the settings of the user.
func (settingsHandlers) GetSettings(_ context.Context, _ GetSettingsRequestObject) (GetSettingsResponseObject, error) {
	return nil, notImplemented()
}

// UpdateSettings replaces the settings of the user.
func (settingsHandlers) UpdateSettings(_ context.Context, _ UpdateSettingsRequestObject) (UpdateSettingsResponseObject, error) {
	return nil, notImplemented()
}

// GetUsage returns the extraction pages used this month and the limit.
func (settingsHandlers) GetUsage(_ context.Context, _ GetUsageRequestObject) (GetUsageResponseObject, error) {
	return nil, notImplemented()
}
