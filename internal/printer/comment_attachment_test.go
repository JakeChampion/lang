package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// The formatter's contract for comments, from the syntax reference: "The
// formatter preserves their original position." #6335 is what happens when
// that is gated by goldens only — a comment trailing an enum variant was
// detached and re-emitted above an unrelated struct written later in the
// file, `-fmt -w` wrote it to disk, and `make fmt-check` stayed green because
// it only ever formats files already in the formatter's fixed point.
//
// So this gate is PROPERTY-shaped, not golden-shaped: for each comment in the
// input it computes an ANCHOR — the code the comment is attached to — and
// requires the same anchor in the output. That covers shapes nobody thought
// to write a golden for, which is the class the bug lived in.
//
// The anchor is the first identifier-ish token on the comment's own line
// before it (a trailing comment: `Unclosed(i32),  // …` anchors to
// "Unclosed"), or, for a comment on its own line, the first such token on the
// next line that carries code (a leading comment anchors to what it
// introduces). Comparing anchors rather than whole lines survives the
// reflowing the formatter legitimately does.

// commentAnchor returns the anchor for the comment on line i of lines, or ""
// when there is no code to anchor to (a comment at end of file).
func commentAnchor(lines []string, i int) string {
	if tok := firstToken(codeBefore(lines[i])); tok != "" {
		return tok
	}
	for j := i + 1; j < len(lines); j++ {
		code := codeBefore(lines[j])
		if strings.TrimSpace(code) == "" {
			continue // blank, or another comment line
		}
		return firstToken(code)
	}
	return ""
}

// codeBefore returns the part of a line before its `//`, or "" if the line is
// entirely a comment. A `//` inside a string literal would fool this; the
// corpus below deliberately contains none, and a false anchor would make the
// test stricter, not weaker.
func codeBefore(line string) string {
	k := strings.Index(line, "//")
	if k < 0 {
		return line
	}
	return line[:k]
}

// firstToken returns the first run of identifier characters in s.
func firstToken(s string) string {
	start := -1
	for i, r := range s {
		isIdent := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if isIdent && start < 0 {
			start = i
		} else if !isIdent && start >= 0 {
			return s[start:i]
		}
	}
	if start >= 0 {
		return s[start:]
	}
	return ""
}

// commentAnchors maps each comment's text to its anchor, in order.
type anchored struct {
	text   string
	anchor string
}

func commentAnchors(src string) []anchored {
	lines := strings.Split(src, "\n")
	var out []anchored
	for i, line := range lines {
		k := strings.Index(line, "//")
		if k < 0 {
			continue
		}
		out = append(out, anchored{
			text:   strings.TrimSpace(line[k:]),
			anchor: commentAnchor(lines, i),
		})
	}
	return out
}

var commentAttachmentCorpus = []struct {
	name string
	src  string
}{
	{"enum-variant-trailing-then-struct", `enum Verdict {
    Balanced,
    Unexpected(i32),        // closer at pos, nothing open
    Unclosed(i32),          // opener never closed
}

struct Report { ok: boolean }

function main(): i32 { return 0; }
`},
	{"struct-field-trailing-then-func", `struct Report {
    ok: boolean,      // did it pass
    n: i32,           // how many
}

function main(): i32 { return 0; }
`},
	{"leading-comments-on-decls", `// about the struct
struct S { a: i32 }

// about the enum
enum E { A, B }

// about main
function main(): i32 { return 0; }
`},
	// The declaration order that made the bug visible: a struct written
	// AFTER an enum. Kind-grouped emission hoisted it above, and the enum's
	// comments went with it.
	{"struct-after-enum", `enum E {
    A,      // first
    B,      // second
}

struct S { a: i32 }

function main(): i32 { return 0; }
`},
	{"func-between-types", `struct A { x: i32 }

// helper
function helper(): i32 { return 1; }

enum E {
    One,    // the one
}

function main(): i32 { return helper(); }
`},
	{"record-variant-trailing", `enum Shape {
    Circle { r: i32 },      // round
    Square(i32),            // boxy
}

function main(): i32 { return 0; }
`},
	{"statement-comments-unchanged", `function main(): i32 {
    var x: i32 = 7;  // Trailing comment.
    // leading comment
    return x;
}
`},
	{"const-and-trait-ordering", `const LIMIT: i32 = 10;

// the trait
trait Speak { function speak(): i32; }

struct Dog { age: i32 }

function main(): i32 { return LIMIT; }
`},
}

// TestFormatPreservesCommentAttachment is the property gate.
func TestFormatPreservesCommentAttachment(t *testing.T) {
	for _, tc := range commentAttachmentCorpus {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := Format(prog)

			want := commentAnchors(tc.src)
			have := commentAnchors(got)
			if len(want) != len(have) {
				t.Fatalf("comment count changed: input %d, output %d\n--- input:\n%s\n--- output:\n%s",
					len(want), len(have), tc.src, got)
			}
			for i := range want {
				if want[i].text != have[i].text {
					t.Errorf("comment %d text changed: %q -> %q", i, want[i].text, have[i].text)
					continue
				}
				if want[i].anchor != have[i].anchor {
					t.Errorf("comment %q moved: attached to %q in the input, %q in the output\n--- output:\n%s",
						want[i].text, want[i].anchor, have[i].anchor, got)
				}
			}
		})
	}
}

// TestFormatKeepsDeclarationOrder pins the other half of #6335: declarations
// emit in SOURCE order. The kind-grouped emission that preceded it is what
// desynchronised the comment cursor in the first place, so a regression here
// would silently re-arm the attachment bug even with the gate above passing
// on its own corpus.
func TestFormatKeepsDeclarationOrder(t *testing.T) {
	src := `function first(): i32 { return 1; }

struct Second { a: i32 }

const THIRD: i32 = 3;

enum Fourth { X }

function fifth(): i32 { return 5; }
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := Format(prog)
	order := []string{"first", "Second", "THIRD", "Fourth", "fifth"}
	at := make([]int, len(order))
	for i, name := range order {
		at[i] = strings.Index(got, name)
		if at[i] < 0 {
			t.Fatalf("%q missing from the formatted output:\n%s", name, got)
		}
	}
	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Errorf("declarations reordered: %q emitted before %q\n%s", order[i], order[i-1], got)
		}
	}
}

// TestFormatCommentAttachmentIsIdempotent — formatting the formatted output
// must not move a comment either. A one-pass gate would miss an attachment
// that is correct once and drifts on the second pass, which is exactly what
// `-fmt -w` run twice would commit.
func TestFormatCommentAttachmentIsIdempotent(t *testing.T) {
	for _, tc := range commentAttachmentCorpus {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			once := Format(prog)
			prog2, err := parser.Parse(once)
			if err != nil {
				t.Fatalf("reparse of formatted output: %v\n%s", err, once)
			}
			twice := Format(prog2)
			if once != twice {
				t.Errorf("not idempotent:\n--- once:\n%s\n--- twice:\n%s", once, twice)
			}
		})
	}
}
