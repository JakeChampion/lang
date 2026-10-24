package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func commentAnchor(lines []string, i int) string {
	if tok := firstToken(codeBefore(lines[i])); tok != "" {
		return tok
	}
	for j := i + 1; j < len(lines); j++ {
		code := codeBefore(lines[j])
		if strings.TrimSpace(code) == "" {
			continue
		}
		return firstToken(code)
	}
	return ""
}

func codeBefore(line string) string {
	k := strings.Index(line, "//")
	if k < 0 {
		return line
	}
	return line[:k]
}

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

func commentRunShapes(src string) []string {
	var out []string
	cur, hasComment := "", false
	flush := func() {
		if hasComment {
			out = append(out, strings.Trim(cur, "B"))
		}
		cur, hasComment = "", false
	}
	for _, line := range strings.Split(src, "\n") {
		tl := strings.TrimSpace(line)
		switch {
		case tl == "":
			if !strings.HasSuffix(cur, "B") {
				cur += "B"
			}
		case strings.HasPrefix(tl, "//"):
			cur += "C"
			hasComment = true
		default:
			flush()
		}
	}
	flush()
	return out
}

func TestCommentRunShapesSeesACollapsedGap(t *testing.T) {
	apart := commentRunShapes("// section\n\n\n// doc\nfunction f(): i32 { return 0; }\n")
	merged := commentRunShapes("// section\n// doc\nfunction f(): i32 { return 0; }\n")
	if strings.Join(apart, ",") != "CBC" || strings.Join(merged, ",") != "CC" {
		t.Fatalf("shapes = %v and %v, want [CBC] and [CC]", apart, merged)
	}
}

var commentAttachmentCorpus = []struct {
	name string
	src  string
}{
	{"trait-and-impl-member-comments", `trait Shape {
    // The area, in whatever unit the shape was measured in.
    function area(self: Self): i32;

    function name(self: Self): string;  // for the report
}

struct Sq { s: i32 }

impl Shape for Sq {
    function area(self: Sq): i32 { return self.s * self.s; }

    // A square is named after its side.
    function name(self: Sq): string { return "square"; }
}

function main(): i32 { return 0; }
`},
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
    let x: i32 = 7;  // Trailing comment.
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
	{"late-import", `import "core/map";

// about first
function first(): i32 { return 1; }

// about Pair
struct Pair { a: i32, b: i32 }

// about the late import
import "std/strings"; // trailing on the import
pub use "./util".{helper};

// about second
function second(): i32 { return 2; }
`},
}

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

func TestFormatKeepsLateImportInPlace(t *testing.T) {
	src := `import "core/map";
// about first
function first(): i32 { return 1; }
// about the late imports
import "std/strings";
import "./util" as u;
// about second
function second(): i32 { return 2; }
`
	want := `import "core/map";

// about first
function first(): i32 {
  return 1;
}

// about the late imports
import "std/strings";
import "./util" as u;

// about second
function second(): i32 {
  return 2;
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := Format(prog); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatKeepsSectionCommentApart(t *testing.T) {
	src := `// --- section one ---

// about first
function first(): i32 { return 1; }

// --- section two ---
//
// prose about the section

// about Pair
struct Pair { a: i32 }

// a note about what follows

const LIMIT: i32 = 3;

// a note above an attribute

@inline
function third(): i32 { return 3; }
`
	want := `// --- section one ---

// about first
function first(): i32 {
  return 1;
}

// --- section two ---
//
// prose about the section

// about Pair
struct Pair { a: i32 }

// a note about what follows

const LIMIT: i32 = 3;

// a note above an attribute

@inline
function third(): i32 {
  return 3;
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := Format(prog); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatKeepsCommentGapsBelowTopLevel(t *testing.T) {
	src := `function f(): i32 {
  // section

  // doc of x
  let x: i32 = 1;
  return x;
}

struct S {
  // group

  // doc a
  a: i32,
}

// trailing a

// trailing b
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := Format(prog); got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

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
