package diag

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

type fakeErr struct {
	pos ast.Position
	msg string
}

func (e *fakeErr) Error() string          { return "type error at " + e.pos.String() + ": " + e.msg }
func (e *fakeErr) Position() ast.Position { return e.pos }

type fakeSpanErr struct {
	fakeErr
	span int
}

func (e *fakeSpanErr) Length() int { return e.span }

type fakeHintErr struct {
	fakeErr
	hint string
}

func (e *fakeHintErr) Hint() string { return e.hint }

func TestFormatRendersSnippetAndCaret(t *testing.T) {
	src := "function f() {\n    return x + 1;\n}\n"
	out := Format("", src, &fakeErr{pos: ast.Position{Line: 2, Col: 12}, msg: "undefined identifier \"x\""})
	want := "2:12: error: undefined identifier \"x\"\n    " +
		"    return x + 1;\n" +
		"               ^"
	if out != want {
		t.Errorf("rendered:\n%s\n--- want ---\n%s", out, want)
	}
}

func TestFormatIncludesFilename(t *testing.T) {
	src := "function f(): void {}\n"
	out := Format("foo.fern", src, &fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "boom"})
	if !strings.HasPrefix(out, "foo.fern:1:1: error: boom\n") {
		t.Errorf("expected filename prefix in:\n%s", out)
	}
}

func TestFormatSpanRendersSquiggle(t *testing.T) {
	src := "var hello = 1;\n"
	e := &fakeSpanErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 5}, msg: "bad"},
		span:    5, // "hello"
	}
	out := Format("", src, e)
	if !strings.Contains(out, "    ^~~~") {
		t.Errorf("expected ^~~~ span, got:\n%s", out)
	}
}

func TestFormatSpanCappedToLine(t *testing.T) {
	// span larger than the line shouldn't run off the right edge.
	src := "abc\n"
	e := &fakeSpanErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "bad"},
		span:    99,
	}
	out := Format("", src, e)
	last := out[strings.LastIndex(out, "\n")+1:]
	if len(strings.TrimSpace(last)) > len("abc") {
		t.Errorf("squiggle ran past EOL: %q", last)
	}
}

func TestFormatHintRenderedAsNote(t *testing.T) {
	src := "x\n"
	e := &fakeHintErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "bad"},
		hint:    `did you mean "y"?`,
	}
	out := Format("", src, e)
	if !strings.Contains(out, `note: did you mean "y"?`) {
		t.Errorf("expected note line, got:\n%s", out)
	}
}

func TestFormatPlainErrorFallback(t *testing.T) {
	out := Format("", "source", errors.New("boom"))
	if out != "boom" {
		t.Errorf("got %q, want %q", out, "boom")
	}
}

func TestErrorsAggregates(t *testing.T) {
	es := Errors{
		&fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "first"},
		&fakeErr{pos: ast.Position{Line: 2, Col: 1}, msg: "second"},
	}
	out := Format("", "a\nb\n", es)
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("expected both errors, got:\n%s", out)
	}
}

func TestPickLine(t *testing.T) {
	src := "alpha\nbeta\ngamma"
	if l := pickLine(src, 2); l != "beta" {
		t.Errorf("line 2 = %q, want \"beta\"", l)
	}
	if l := pickLine(src, 3); l != "gamma" {
		t.Errorf("line 3 (no trailing newline) = %q, want \"gamma\"", l)
	}
	if l := pickLine(src, 99); l != "" {
		t.Errorf("line 99 should be empty, got %q", l)
	}
}

func TestTabsBecomeSpaces(t *testing.T) {
	src := "\tcode here\n"
	out := pickLine(src, 1)
	if strings.Contains(out, "\t") {
		t.Errorf("expected tabs replaced, got %q", out)
	}
}

func TestSuggestPicksClosest(t *testing.T) {
	cands := []string{"length", "left", "lenght"}
	if got := Suggest("lenght", cands); got != "length" {
		// "lenght" is in the candidate list but Suggest skips exact matches,
		// so the next-closest is "length" (edit distance 2 → swap of n/g).
		t.Errorf("got %q, want %q", got, "length")
	}
}

func TestSuggestReturnsEmptyOutsideBudget(t *testing.T) {
	if got := Suggest("foobar", []string{"completelyDifferent"}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestSuggestShortNamesUseTighterBudget(t *testing.T) {
	// "ab" → "yz" has distance 2; for short names we only allow 1.
	if got := Suggest("ab", []string{"yz"}); got != "" {
		t.Errorf("got %q, want empty for short name above budget", got)
	}
	if got := Suggest("ab", []string{"ax"}); got != "ax" {
		t.Errorf("got %q, want \"ax\"", got)
	}
}

// fakeLabeledErr exercises the Labeled interface for the
// multi-label diagnostic renderer (docs/DIAGNOSTIC-UX-RESEARCH.md
// Rec §1). Pretends to be a checker-emitted error that points
// at both the use site (primary) and the declaration site
// (secondary).
type fakeLabeledErr struct {
	fakeErr
	labels []Label
}

func (e *fakeLabeledErr) Labels() []Label { return e.labels }

func TestFormatRendersMultiLabel(t *testing.T) {
	src := "function f(): i32 {\n    var x: i32 = 1;\n    x = \"oops\";\n    return x;\n}\n"
	err := &fakeLabeledErr{
		fakeErr: fakeErr{
			pos: ast.Position{Line: 3, Col: 9},
			msg: "cannot assign string to i32",
		},
		labels: []Label{
			{Pos: ast.Position{Line: 3, Col: 9}, Length: 6, Message: "expected i32 here", Kind: LabelPrimary},
			{Pos: ast.Position{Line: 2, Col: 12}, Length: 3, Message: "declared with this type", Kind: LabelSecondary},
		},
	}
	out := Format("", src, err)
	// Primary label rendered like a normal Positioned error.
	if !strings.Contains(out, "3:9: error: cannot assign string to i32") {
		t.Errorf("missing primary header in:\n%s", out)
	}
	// Secondary label gets its own header with `note:` tag.
	if !strings.Contains(out, "2:12: note: declared with this type") {
		t.Errorf("missing secondary header in:\n%s", out)
	}
	// Secondary underline uses `-` not `^~`.
	if !strings.Contains(out, "---") {
		t.Errorf("secondary should have `---` underline:\n%s", out)
	}
}

func TestFormatRendersHelpLabel(t *testing.T) {
	src := "var x = 1;\n"
	err := &fakeLabeledErr{
		fakeErr: fakeErr{
			pos: ast.Position{Line: 1, Col: 5},
			msg: "type annotation required",
		},
		labels: []Label{
			{Pos: ast.Position{Line: 1, Col: 5}, Length: 1, Message: "annotation goes here", Kind: LabelPrimary},
			{Pos: ast.Position{Line: 1, Col: 5}, Length: 1, Message: "try `var x: i32 = 1;`", Kind: LabelHelp},
		},
	}
	out := Format("", src, err)
	// Help labels get `help:` prefix.
	if !strings.Contains(out, "1:5: help: try `var x: i32 = 1;`") {
		t.Errorf("missing help label in:\n%s", out)
	}
}

// Single-label errors (only the primary, no secondaries) should
// render IDENTICALLY to non-Labeled errors. Regression sentinel:
// migrating an existing error to satisfy Labeled mustn't break
// downstream consumers (tests, golden output, etc.).
func TestFormatLabeledWithOnlyPrimaryMatchesNonLabeled(t *testing.T) {
	src := "function f() {\n    return x + 1;\n}\n"
	pos := ast.Position{Line: 2, Col: 12}
	plain := &fakeErr{pos: pos, msg: "undefined identifier \"x\""}
	labeled := &fakeLabeledErr{
		fakeErr: *plain,
		labels: []Label{
			{Pos: pos, Length: 1, Message: "undefined identifier \"x\"", Kind: LabelPrimary},
		},
	}
	if Format("", src, plain) != Format("", src, labeled) {
		t.Errorf("primary-only Labeled output diverges from non-Labeled output\n plain:\n%s\nlabeled:\n%s",
			Format("", src, plain), Format("", src, labeled))
	}
}

// Filename routing: secondary labels include the filename in
// the same shape as the primary header, so cross-file labels
// (`declared in lib/foo.fern`) render with the right path.
func TestFormatLabeledIncludesFilenameOnSecondary(t *testing.T) {
	src := "var x: i32 = 1;\nx = true;\n"
	err := &fakeLabeledErr{
		fakeErr: fakeErr{
			pos: ast.Position{Line: 2, Col: 1},
			msg: "cannot assign boolean to i32",
		},
		labels: []Label{
			{Pos: ast.Position{Line: 2, Col: 1}, Length: 1, Message: "primary", Kind: LabelPrimary},
			{Pos: ast.Position{Line: 1, Col: 8}, Length: 3, Message: "declared here", Kind: LabelSecondary},
		},
	}
	out := Format("/abs/path.fern", src, err)
	if !strings.Contains(out, "/abs/path.fern:2:1: error:") {
		t.Errorf("primary missing /abs/path.fern prefix:\n%s", out)
	}
	if !strings.Contains(out, "/abs/path.fern:1:8: note: declared here") {
		t.Errorf("secondary missing filename prefix:\n%s", out)
	}
}

// fakeCodedErr — pretends to be a checker error with a stable
// code stamped. Exercises Coded interface + the header
// `error[CODE]: msg` rendering.
type fakeCodedErr struct {
	fakeErr
	code string
}

func (e *fakeCodedErr) Code() string { return e.code }

func TestFormatRendersErrorCode(t *testing.T) {
	src := "function f() { return x; }\n"
	err := &fakeCodedErr{
		fakeErr: fakeErr{
			pos: ast.Position{Line: 1, Col: 23},
			msg: "undefined identifier \"x\"",
		},
		code: "E001",
	}
	out := Format("", src, err)
	if !strings.Contains(out, "1:23: error[E001]: undefined identifier") {
		t.Errorf("missing coded header in:\n%s", out)
	}
}

func TestFormatEmptyCodeFallsBackToPlainError(t *testing.T) {
	src := "function f() { return x; }\n"
	err := &fakeCodedErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 23}, msg: "undefined identifier \"x\""},
		code:    "",
	}
	out := Format("", src, err)
	// Empty code shouldn't render `error[]:` — falls back to plain.
	if strings.Contains(out, "error[]:") {
		t.Errorf("empty Code() should not produce `error[]:`:\n%s", out)
	}
	if !strings.Contains(out, "1:23: error: undefined identifier") {
		t.Errorf("missing plain header in:\n%s", out)
	}
}

func TestExplainReturnsCatalogueBody(t *testing.T) {
	body := Explain("E001")
	if body == "" {
		t.Fatal("Explain(\"E001\") returned empty — markdown not embedded?")
	}
	if !strings.Contains(body, "Undefined identifier") {
		t.Errorf("Explain body missing canonical title:\n%s", body)
	}
}

func TestExplainCaseInsensitive(t *testing.T) {
	if Explain("e001") == "" {
		t.Error("Explain(\"e001\") should match E001 case-insensitively")
	}
	if Explain("  E001  ") == "" {
		t.Error("Explain should trim whitespace")
	}
}

func TestExplainUnknownReturnsEmpty(t *testing.T) {
	if Explain("E999") != "" {
		t.Error("Explain(\"E999\") should return empty for unknown code")
	}
	if Explain("") != "" {
		t.Error("Explain(\"\") should return empty")
	}
}

func TestAvailableCodesEnumeratesCatalogue(t *testing.T) {
	codes := AvailableCodes()
	if len(codes) == 0 {
		t.Fatal("AvailableCodes() returned empty — no markdown files found")
	}
	// Phase 1-11 catalogue: 47 checker codes + 3 parser codes.
	wantSet := map[string]bool{
		"E001": true, "E002": true, "E003": true, "E004": true, "E005": true,
		"E006": true, "E007": true, "E008": true, "E009": true, "E010": true,
		"E011": true, "E012": true, "E013": true, "E014": true, "E015": true,
		"E016": true, "E017": true, "E018": true, "E019": true, "E020": true,
		"E021": true, "E022": true, "E023": true, "E024": true,
		"E026": true, "E027": true, "E028": true, "E029": true, "E030": true,
		"E031": true, "E032": true, "E033": true, "E034": true, "E035": true,
		"E036": true, "E037": true, "E038": true, "E039": true, "E040": true,
		"E041": true, "E042": true, "E043": true, "E044": true, "E045": true,
		"E046": true, "E047": true, "E048": true, "E049": true, "E050": true,
		"E051": true, "E052": true, "E053": true, "E054": true, "E055": true,
		"E056": true, "E057": true, "E058": true, "E059": true, "E060": true,
		"E061": true, "E062": true, "E063": true, "E064": true,
		"P001": true, "P002": true, "P003": true,
	}
	gotSet := map[string]bool{}
	for _, c := range codes {
		gotSet[c] = true
	}
	for c := range wantSet {
		if !gotSet[c] {
			t.Errorf("AvailableCodes() missing %q; got %v", c, codes)
		}
	}
}

func TestFormatExplainWrapsBody(t *testing.T) {
	body := "explanation body\n"
	got := FormatExplain("E001", body)
	if !strings.HasPrefix(got, "error E001:") {
		t.Errorf("FormatExplain prefix mismatch:\n%s", got)
	}
	if !strings.Contains(got, body) {
		t.Errorf("FormatExplain dropped body:\n%s", got)
	}
}

// FormatRemapped with a nil remap is identical to Format.
func TestFormatRemappedNilIsIdentity(t *testing.T) {
	src := "function f() {\n    return x + 1;\n}\n"
	e := &fakeErr{pos: ast.Position{Line: 2, Col: 12}, msg: "undefined identifier \"x\""}
	if got, want := FormatRemapped("", src, nil, e), Format("", src, e); got != want {
		t.Errorf("FormatRemapped(nil) =\n%s\n--- want (Format) ---\n%s", got, want)
	}
}

// FormatRemapped rewrites the reported line/column and looks the source
// line up in displaySrc using the remapped line — the literate case
// where the checker reports a position in tangled source but the
// document line is elsewhere.
func TestFormatRemappedRewritesPosition(t *testing.T) {
	// displaySrc is the ".fern.md" document; the offending code lives on
	// document line 4 with 4 columns of tangling indentation to undo.
	displaySrc := "# Title\n\n```fern\n    let x = y;\n```\n"
	// The checker saw it on generated line 2, column 9 (= doc col 5 + 4
	// of added indent).
	remap := func(p ast.Position) ast.Position {
		if p.Line == 2 {
			return ast.Position{Line: 4, Col: p.Col - 4}
		}
		return p
	}
	e := &fakeErr{pos: ast.Position{Line: 2, Col: 9}, msg: "undefined identifier \"y\""}
	out := FormatRemapped("prog.fern.md", displaySrc, remap, e)
	if !strings.HasPrefix(out, "prog.fern.md:4:5: error:") {
		t.Errorf("expected remapped header prog.fern.md:4:5, got:\n%s", out)
	}
	// The rendered snippet must be the document line, not the generated one.
	if !strings.Contains(out, "    let x = y;") {
		t.Errorf("expected document source line in output:\n%s", out)
	}
}

// Secondary/help label positions are remapped too.
func TestFormatRemappedRemapsLabels(t *testing.T) {
	displaySrc := "aaa\nbbb\nccc\nddd\neee\n"
	remap := func(p ast.Position) ast.Position {
		return ast.Position{Line: p.Line + 2, Col: p.Col} // shift every line by 2
	}
	e := &fakeLabeledErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "primary"},
		labels: []Label{
			{Pos: ast.Position{Line: 1, Col: 1}, Message: "primary", Kind: LabelPrimary},
			{Pos: ast.Position{Line: 2, Col: 1}, Message: "see here", Kind: LabelSecondary},
		},
	}
	out := FormatRemapped("", displaySrc, remap, e)
	// Primary header: line 1 -> 3.
	if !strings.HasPrefix(out, "3:1: error: primary") {
		t.Errorf("primary not remapped to line 3:\n%s", out)
	}
	// Secondary label: line 2 -> 4, and its snippet line is "ddd".
	if !strings.Contains(out, "4:1: note: see here") {
		t.Errorf("secondary label not remapped to line 4:\n%s", out)
	}
	if !strings.Contains(out, "ddd") {
		t.Errorf("secondary label snippet should be document line 4 (ddd):\n%s", out)
	}
}
