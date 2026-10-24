package diag

import "strings"

var terse bool

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
