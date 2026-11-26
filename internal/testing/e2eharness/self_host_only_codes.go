package e2eharness

import "regexp"

// selfHostOnlyCodes are the diagnostic codes that only the self-host checker
// reports. The Go checker does not implement the ownership rules (#16331).
var selfHostOnlyCodes = map[string]bool{"E050": true, "E051": true}

var diagCodeRE = regexp.MustCompile(`\bE\d{3}\b`)

// SelfHostOnlyCode reports a code that the Go checker does not report.
func SelfHostOnlyCode(code string) bool { return selfHostOnlyCodes[code] }

// NamesSelfHostOnlyCode reports a text that names a code that only the
// self-host checker reports.
func NamesSelfHostOnlyCode(text string) bool {
	for _, c := range diagCodeRE.FindAllString(text, -1) {
		if selfHostOnlyCodes[c] {
			return true
		}
	}
	return false
}

// SharedCodes returns codes without the codes that only the self-host checker
// reports. A comparison of the two checkers compares these.
func SharedCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if !selfHostOnlyCodes[c] {
			out = append(out, c)
		}
	}
	return out
}
