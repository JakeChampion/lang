// Package diag renders compiler errors against the original source. It
// turns a position+message pair (which lexer, parser and checker errors
// already carry) into output like:
//
//	path/foo.fern:3:9: error: undefined identifier "x"
//	    return x + 1;
//	           ^
//	    note: did you mean "y"?
//
// Errors aggregates multiple per-error diagnostics so the type checker
// can report several problems at once.
package diag

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Positioned is satisfied by every error in this codebase that has a
// source location. Renderers use it to extract the line/column, fall
// back to the wrapped error's message via Error(), and pretty-print.
type Positioned interface {
	error
	Position() ast.Position
}

// Spanned is optionally satisfied by errors that know the length of
// the offending token. When present the underline becomes `^~~~~`
// covering the whole span instead of a single caret.
type Spanned interface {
	Positioned
	Length() int
}

// Hinted is optionally satisfied by errors that want to attach a
// follow-up note to the diagnostic — typically a "did you mean foo?"
// suggestion. The hint is rendered on its own line with a `note:`
// prefix.
type Hinted interface {
	error
	Hint() string
}

// Filed is optionally satisfied by errors that know which source
// file they were emitted against. modload stamps this on parser /
// lexer errors after each module's parse so cross-file diagnostics
// can be routed back to the right URI in workspace-mode LSP. The
// checker fills it from `c.current.SourceModule` whenever the
// emitting context is a known FuncDecl. Empty string means
// "unknown" — callers should fall back to the entry file.
type Filed interface {
	error
	File() string
}

// Coded is optionally satisfied by errors that carry a stable
// error code (docs/DIAGNOSTIC-UX-RESEARCH.md Rec §4). The code
// is rendered in the header — `error[E001]: …` — so users can
// search for it and look up the long-form explanation via
// `lang explain E001`. Codes are stable identifiers; the
// rendered phrasing of the error message can evolve without
// breaking the search.
//
// Empty Code() means "no stable code assigned" — the renderer
// falls back to the plain `error:` prefix. Existing errors
// stay unaffected until they opt in.
type Coded interface {
	error
	Code() string
}

// LabelKind tags a Label's role in a multi-label diagnostic
// (docs/DIAGNOSTIC-UX-RESEARCH.md Rec §1).
//
//	Primary   — the "error happens here" pointer; rendered with `^`.
//	Secondary — context that informs the primary (e.g. "declared
//	            with this type" pointing at the original decl);
//	            rendered with `--` underline + "note:" prefix.
//	Help      — actionable explanation or suggested fix; rendered
//	            with `--` + "help:" prefix. Distinct from
//	            Secondary so the IDE can surface help labels as
//	            CodeAction candidates while leaving Secondary as
//	            plain `relatedInformation`.
type LabelKind int

const (
	LabelPrimary LabelKind = iota
	LabelSecondary
	LabelHelp
)

// Label is one source-location annotation in a multi-label
// diagnostic. Spans use the (Position, Length) shape already
// established by the Spanned interface — a length of 0 means
// "single caret at the position."
type Label struct {
	Pos     ast.Position
	Length  int
	Message string
	Kind    LabelKind
}

// Suggestion is a machine-applicable fix (docs/DIAGNOSTIC-UX-RESEARCH.md
// Rec §3): replacing Length bytes of source at Pos with Replacement is
// guaranteed BY THE EMITTER to produce a program that re-parses — an
// unsound suggestion is worse than none, so only exact rewrites (e.g. an
// identifier respelling) attach one. Title is the human-readable line the
// renderer prints after `help:`; the span/replacement pair is the machine
// half (the future LSP CodeAction seed).
type Suggestion struct {
	Pos         ast.Position
	Length      int
	Replacement string
	Title       string
}

// Suggested is satisfied by errors carrying a machine-applicable fix.
// A nil Suggestion means none.
type Suggested interface {
	error
	Suggestion() *Suggestion
}

// Labeled is satisfied by errors that carry multiple source
// annotations (typically primary-at-use plus secondary-at-decl).
// The renderer falls back to the Positioned / Spanned single-
// label path when an error implements only those.
//
// Convention: Labels() should return the primary label FIRST.
// The renderer uses the primary's position for the header
// `path:L:C: error: msg` line; secondary / help labels render
// below with their own line/caret pair.
type Labeled interface {
	error
	Labels() []Label
}

// WithFile walks err and stamps `filename` on every entry that
// implements `error` and exposes a `*string` SetFile mutator (via
// the unexported setFile interface concrete error types implement).
// Used by modload to attribute parser / lexer errors to the module
// they came from. Single errors and diag.Errors slices are both
// handled.
func WithFile(err error, filename string) error {
	if err == nil {
		return nil
	}
	if es, ok := err.(Errors); ok {
		for _, e := range es {
			stampFile(e, filename)
		}
		return es
	}
	stampFile(err, filename)
	return err
}

// setFile is the package-private mutator concrete error types
// satisfy to let WithFile poke their File field. Kept unexported
// so consumers can't paint random errors with bogus paths.
type setFile interface {
	setFile(filename string)
}

func stampFile(err error, filename string) {
	if s, ok := err.(setFile); ok {
		s.setFile(filename)
	}
}

// Errors is a flat collection of errors. Useful when a pass (e.g. the
// type checker) wants to report many problems in one go.
type Errors []error

func (es Errors) Error() string {
	switch len(es) {
	case 0:
		return ""
	case 1:
		return es[0].Error()
	}
	var b strings.Builder
	for i, e := range es {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

// As lets `errors.As` traverse Errors. It picks the first match.
func (es Errors) As(target any) bool {
	for _, e := range es {
		if as, ok := e.(interface{ As(any) bool }); ok && as.As(target) {
			return true
		}
	}
	return false
}

// Format renders err with the relevant source line and a caret. If err
// is an Errors, every entry is rendered. Errors that don't satisfy
// Positioned fall back to plain Error() output. The filename is
// included in the header (Unix-tool style) when non-empty; it's
// otherwise omitted so unit tests can render without a path prefix.
func Format(filename, src string, err error) string {
	return format(filename, src, nil, err)
}

// FormatRemapped is Format with a position-remapping hook. Every source
// position (the primary location and any secondary/help labels) is
// passed through `remap` before rendering, and the source line is
// looked up in `displaySrc` using the *remapped* line number. It exists
// for literate programs: the checker reports positions in the tangled
// (generated) source, but diagnostics should point at the line the
// author wrote in the `.fern.md` document. Pass the literate source as
// displaySrc and a closure built from the tangle line map as remap. A
// nil remap behaves exactly like Format.
func FormatRemapped(filename, displaySrc string, remap func(ast.Position) ast.Position, err error) string {
	return format(filename, displaySrc, remap, err)
}

func format(filename, src string, remap func(ast.Position) ast.Position, err error) string {
	if err == nil {
		return ""
	}
	if es, ok := err.(Errors); ok {
		var b strings.Builder
		for i, e := range es {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(format(filename, src, remap, e))
		}
		return b.String()
	}

	pe, ok := err.(Positioned)
	if !ok {
		return err.Error()
	}
	pos := pe.Position()
	if remap != nil {
		pos = remap(pos)
	}
	line := pickLine(src, pos.Line)
	if line == "" && pos.Line == 0 {
		// Position-less but code-carrying errors (capability enforcement
		// — E066 reached via an imported module, E070's cross-package
		// chains) still get the `error[EXXX]:` header; the message body
		// carries the attribution a caret cannot.
		if cd, ok := err.(Coded); ok {
			if code := cd.Code(); code != "" {
				return paint(ansiRedBld, "error["+code+"]") + ": " + stripPrefix(pe.Error())
			}
		}
		return err.Error()
	}

	// `error[E001]: …` shape when the error carries a stable
	// code (docs/DIAGNOSTIC-UX-RESEARCH.md Rec §4). Plain
	// `error: …` otherwise — keeps every non-Coded error
	// rendering unchanged.
	prefix := "error"
	if cd, ok := err.(Coded); ok {
		if code := cd.Code(); code != "" {
			prefix = "error[" + code + "]"
		}
	}
	header := fmt.Sprintf("%d:%d: %s: %s", pos.Line, pos.Col, paint(ansiRedBld, prefix), stripPrefix(pe.Error()))
	if filename != "" {
		header = filename + ":" + header
	}

	pad := strings.Repeat(" ", clamp(pos.Col-1, 0, len(line)))
	mark := "^"
	if sp, ok := err.(Spanned); ok && sp.Length() > 1 {
		// Cap the squiggle to what fits on the line so it never wraps.
		room := len(line) - (pos.Col - 1) - 1
		if room < 0 {
			room = 0
		}
		span := sp.Length()
		if span-1 > room {
			span = room + 1
		}
		mark = "^" + strings.Repeat("~", span-1)
	}

	// Rich (colour) mode renders a right-aligned line-number gutter with a
	// box-drawing separator (docs/DIAGNOSTIC-UX-RESEARCH.md Rec §7); the
	// classic path is byte-for-byte unchanged so every non-interactive
	// caller sees the historical layout. The note aligns under the gutter.
	coloredMark := paint(ansiRed, mark)
	var out, notePrefix string
	if enableColor {
		num := fmt.Sprintf("%d", pos.Line)
		blank := strings.Repeat(" ", len(num))
		sep := paint(ansiBlue, boxVert())
		out = fmt.Sprintf("%s\n %s %s %s\n %s %s %s%s", header, num, sep, line, blank, sep, pad, coloredMark)
		notePrefix = " " + blank + " " + sep + " "
	} else {
		out = fmt.Sprintf("%s\n    %s\n    %s%s", header, line, pad, coloredMark)
		notePrefix = "    "
	}
	if h, ok := err.(Hinted); ok {
		if hint := h.Hint(); hint != "" {
			out += "\n" + notePrefix + paint(ansiBlue, "note:") + " " + hint
		}
	}
	// Machine-applicable fix (Rec §3): an actionable `help:` line. The
	// structured span/replacement stays on the error for machine
	// consumers; the emitter guarantees applying it re-parses.
	if sg, ok := err.(Suggested); ok {
		if fix := sg.Suggestion(); fix != nil && fix.Title != "" {
			out += "\n" + notePrefix + paint(ansiBlue, "help:") + " " + fix.Title
		}
	}
	// Multi-label diagnostic (docs/DIAGNOSTIC-UX-RESEARCH.md
	// Rec §1). Labels()[0] is the primary; the header above
	// already rendered it. Render the remaining labels with
	// their own line/underline pair and a kind-tagged prefix.
	if lb, ok := err.(Labeled); ok {
		labels := lb.Labels()
		for i, l := range labels {
			if i == 0 {
				continue
			}
			if remap != nil {
				l.Pos = remap(l.Pos)
			}
			out += "\n" + renderSecondaryLabel(filename, src, l)
		}
	}
	return out
}

// renderSecondaryLabel formats one Secondary/Help label below
// the primary diagnostic. Each carries its own `file:L:C:`
// header so cross-file labels (e.g. "declared in lib/foo.fern"
// pointing at the original decl) route correctly under
// workspace-mode LSP. The underline uses `--` instead of `^~`
// to visually distinguish secondaries from the primary's
// caret.
func renderSecondaryLabel(filename, src string, l Label) string {
	word := "note:"
	markColor := ansiBlue
	if l.Kind == LabelHelp {
		word = "help:"
		markColor = ansiGreen
	}
	tag := paint(markColor, word) + " "
	line := pickLine(src, l.Pos.Line)
	header := fmt.Sprintf("    %d:%d: %s%s", l.Pos.Line, l.Pos.Col, tag, l.Message)
	if filename != "" {
		header = "    " + filename + ":" + fmt.Sprintf("%d:%d: %s%s", l.Pos.Line, l.Pos.Col, tag, l.Message)
	}
	if line == "" {
		return header
	}
	pad := strings.Repeat(" ", clamp(l.Pos.Col-1, 0, len(line)))
	span := l.Length
	if span < 1 {
		span = 1
	}
	room := len(line) - (l.Pos.Col - 1)
	if room < 0 {
		room = 0
	}
	if span > room {
		span = room
	}
	mark := strings.Repeat("-", span)
	if mark == "" {
		mark = "-"
	}
	// Rich mode gives the secondary its own line-number gutter, matching
	// the primary (#4413 Rec §7) so a multi-label diagnostic reads as one
	// consistently-guttered block. Classic keeps the 8-space indent so the
	// plain layout is unchanged.
	if enableColor {
		num := fmt.Sprintf("%d", l.Pos.Line)
		blank := strings.Repeat(" ", len(num))
		sep := paint(ansiBlue, boxVert())
		return fmt.Sprintf("%s\n %s %s %s\n %s %s %s%s", header, num, sep, line, blank, sep, pad, paint(markColor, mark))
	}
	return fmt.Sprintf("%s\n        %s\n        %s%s", header, line, pad, paint(markColor, mark))
}

// stripPrefix removes any "<kind> error at L:C: " prefix that the
// concrete error type may have included, so Format's own header isn't
// duplicated.
func stripPrefix(msg string) string {
	if i := strings.Index(msg, ": "); i >= 0 {
		// Only strip if the prefix looks like "X error at L:C".
		head := msg[:i]
		if strings.Contains(head, " error at ") {
			return msg[i+2:]
		}
	}
	return msg
}

// pickLine returns line `n` (1-based) from src, with tabs replaced by a
// single space so the caret aligns visually.
func pickLine(src string, n int) string {
	if n <= 0 {
		return ""
	}
	cur := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if cur == n {
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src)
			} else {
				end += i
			}
			return strings.ReplaceAll(src[i:end], "\t", " ")
		}
		if src[i] == '\n' {
			cur++
			start = i + 1
		}
	}
	if cur == n {
		return strings.ReplaceAll(src[start:], "\t", " ")
	}
	return ""
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Suggest returns the entry in candidates closest to name within an
// edit-distance budget that scales with name length. An empty result
// means no good suggestion was found and callers should not emit a
// hint at all.
func Suggest(name string, candidates []string) string {
	if name == "" || len(candidates) == 0 {
		return ""
	}
	budget := 2
	if len(name) <= 3 {
		budget = 1
	}
	best := ""
	bestDist := budget + 1
	for _, c := range candidates {
		if c == name {
			continue
		}
		d := levenshtein(name, c)
		if d < bestDist {
			best = c
			bestDist = d
		}
	}
	if bestDist > budget {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
