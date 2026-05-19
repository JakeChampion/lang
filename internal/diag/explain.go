package diag

import (
	"embed"
	"fmt"
	"strings"
)

// explanations is the per-code long-form catalogue. One file
// per code under `internal/diag/explanations/CODE.md`;
// referenced by `lang explain CODE` (docs/DIAGNOSTIC-UX-
// RESEARCH.md Rec §4). Files are markdown so the same source
// renders both at the CLI (plain text) and in future IDE
// surfaces (rich text via the LSP).
//
//go:embed explanations/*.md
var explanations embed.FS

// Explain returns the long-form explanation for the given
// error code. Returns the empty string when the code has no
// catalogue entry; callers should treat that as
// "code unrecognised" and surface a one-liner ("unknown
// error code") rather than an empty output.
//
// Codes are case-insensitive on input — `e001` and `E001`
// both look up the same file — but the catalogue stores
// them uppercase as the canonical form.
func Explain(code string) string {
	if code == "" {
		return ""
	}
	canonical := strings.ToUpper(strings.TrimSpace(code))
	data, err := explanations.ReadFile("explanations/" + canonical + ".md")
	if err != nil {
		return ""
	}
	return string(data)
}

// AvailableCodes returns the catalogued error codes in sort
// order. Used by `lang explain` (no-arg form) to print the
// available list and by tests that walk every code to verify
// the catalogue is consistent with the checker's stamped
// emissions.
func AvailableCodes() []string {
	entries, err := explanations.ReadDir("explanations")
	if err != nil {
		return nil
	}
	var codes []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		codes = append(codes, strings.TrimSuffix(name, ".md"))
	}
	return codes
}

// FormatExplain renders an `Explain` result for CLI output.
// Wraps the markdown body with a one-line header ("error
// EXXX:") + a trailing newline, matching the shape `lang
// explain CODE` writes to stdout. Empty input returns an
// empty string — caller surfaces "unknown code" upstream.
func FormatExplain(code, body string) string {
	if body == "" {
		return ""
	}
	canonical := strings.ToUpper(strings.TrimSpace(code))
	return fmt.Sprintf("error %s:\n\n%s\n", canonical, body)
}
