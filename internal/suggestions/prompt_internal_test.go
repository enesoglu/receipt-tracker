package suggestions

import (
	"strings"
	"testing"
)

// TestPromptVersion_MatchesTheTemplate forces the version to be bumped
// whenever the template changes: a stored suggestion records the version,
// so two prompts must never share one number.
func TestPromptVersion_MatchesTheTemplate(t *testing.T) {
	const wantVersion = 1
	if promptVersion != wantVersion {
		t.Errorf("prompt version = %d, want %d; bump it only together with a template change", promptVersion, wantVersion)
	}
	if !strings.Contains(systemPrompt, `"lines":[{"position"`) {
		t.Error("system prompt no longer describes the answer shape; restore it and keep the version")
	}
	if !strings.Contains(systemPrompt, "never invent a category") {
		t.Error("system prompt no longer forbids invented categories; restore it and keep the version")
	}
}
