package extract

import (
	"regexp"
	"strings"
)

// DisplayName returns the name a line is shown with: the printed name
// without a trailing VAT rate. The provider removes separate markers but
// leaves the ones glued to the name. Only the marker goes; a number before
// it is part of the name ("YAGLI 3%01" becomes "YAGLI 3"). A one-digit
// "%3" stays, because next to a dairy product it is the fat content.
// Unreadable characters are left as they are, never guessed. The printed
// name itself is kept unchanged as the line's raw name.
func DisplayName(raw string) string {
	trailingVATRate := regexp.MustCompile(`\s*%(?:00|01|08|10|18|20)\.?\s*$`)
	name := strings.TrimSpace(trailingVATRate.ReplaceAllString(raw, ""))
	if name == "" {
		return strings.TrimSpace(raw)
	}
	return name
}
