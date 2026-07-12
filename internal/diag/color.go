package diag

// Optional ANSI-colour rendering for diagnostics (docs/DIAGNOSTIC-UX-RESEARCH.md
// Rec §7). Colour is OFF by default so every non-interactive caller — the
// LSP, the differential/golden test harnesses, `fern -check` piped into a
// file — renders exactly the plain text it always has. The `fern` CLI opts
// in via SetColor after deciding from isatty(stderr) + NO_COLOR + --color
// (see cmd/fern/main.go). The toggle is process-global: diagnostics render
// on one goroutine (the CLI's main / the LSP read loop), so no locking is
// needed, matching the renderer's existing stateless-but-single-threaded
// use.

// enableColor gates every paint() call. Package-private; flipped only
// through SetColor.
var enableColor bool

// SetColor turns ANSI-colour rendering on or off for subsequent Format /
// FormatRemapped calls. Returns the previous setting so a caller (or a
// test) can restore it.
func SetColor(on bool) bool {
	prev := enableColor
	enableColor = on
	return prev
}

// ColorEnabled reports whether colour rendering is currently on.
func ColorEnabled() bool { return enableColor }

// ANSI SGR sequences. Kept minimal — severity red for the error label and
// its caret, blue for a `note:` label, green for a `help:` label — the
// palette Rec §7 specifies (colour by severity; suggestions/help stand out).
const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiBlue   = "\x1b[34m"
	ansiBold   = "\x1b[1m"
	ansiRedBld = "\x1b[1;31m"
)

// paint wraps s in the given SGR sequence + reset when colour is enabled,
// and returns s untouched otherwise — so a colour-off render is byte-for-byte
// the historical plain output.
func paint(sgr, s string) string {
	if !enableColor {
		return s
	}
	return sgr + s + ansiReset
}
