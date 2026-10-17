package diag

import "strings"

var terse bool

// SetTerse selects one-line diagnostics without source context, notes, fixes
// or colour. It returns the previous setting.
func SetTerse(on bool) bool {
	previous := terse
	terse = on
	return previous
}

func diagnosticText(text string) string {
	if !terse {
		return text
	}
	return strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(text)
}
