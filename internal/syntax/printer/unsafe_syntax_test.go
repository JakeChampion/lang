package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestUnsafeSyntaxRoundTrip(t *testing.T) {
	for _, prefix := range []string{"", "pub ", "pub(package) ", "fip ", "fbip(2) ", "async ", "@inline\n"} {
		source := prefix + "unsafe function answer(): i32 { unsafe { return 42; } }"
		prog, err := parser.Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		for name, print := range map[string]func(*ast.Program) string{"format": Format, "print": Print} {
			out := print(prog)
			back, err := parser.Parse(out)
			if err != nil {
				t.Fatalf("%s: %v\n%s", name, err, out)
			}
			if name == "format" && ASTShape(prog) != ASTShape(back) {
				t.Fatalf("%s lost syntax: %s\n%s", name, FirstShapeDiff(ASTShape(prog), ASTShape(back)), out)
			}
			if name == "format" && Format(back) != out {
				t.Fatalf("formatter is not idempotent: %s", out)
			}
			if !back.Funcs[0].Unsafe || !back.Funcs[0].Body.Stmts[0].(*ast.Block).IsUnsafe {
				t.Fatalf("%s lost unsafe metadata: %s", name, out)
			}
			if !strings.Contains(out, "unsafe function") || !strings.Contains(out, "unsafe {") {
				t.Fatalf("lost unsafe marker: %s", out)
			}
		}
	}
}

func TestUnsafeTraitSyntaxRoundTrip(t *testing.T) {
	source := "struct Item {} trait Read { unsafe function read(self: Self): i32; } impl Read for Item { unsafe function read(self: Self): i32 { unsafe { return 42; } } }"
	prog, err := parser.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	out := Format(prog)
	back, err := parser.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if ASTShape(prog) != ASTShape(back) {
		t.Fatalf("format lost trait syntax: %s\n%s", FirstShapeDiff(ASTShape(prog), ASTShape(back)), out)
	}
}

func TestUnsafeSyntaxComments(t *testing.T) {
	source := "// Caller obligation.\nunsafe\n// Keep this proof.\nfunction answer(): i32 { unsafe { // Scope proof.\nreturn 42; } }"
	prog, err := parser.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	out := Format(prog)
	for _, comment := range []string{"// Caller obligation.", "// Keep this proof.", "// Scope proof."} {
		if strings.Count(out, comment) != 1 {
			t.Fatalf("lost or duplicated %s\n%s", comment, out)
		}
	}
	back, err := parser.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if Format(back) != out {
		t.Fatalf("comment round trip changed:\n%s\n%s", out, Format(back))
	}
}

// An unsafe block that is the value of a branch, of a match arm or of a
// defer keeps its `unsafe`.
func TestUnsafeBlockValueRoundTrip(t *testing.T) {
	for _, body := range []string{
		`let s = if (ok) { unsafe { raw(b) } } else { ""; }; return s;`,
		`let s = if (ok) { "" } else { unsafe { raw(b) } }; return s;`,
		`return match (find(b)) { Some(x) => unsafe { raw(x) }, None => "" };`,
		`return match (find(b)) { Some(x) => { unsafe { raw(x) } }, None => "" };`,
		`if let Some(x) = find(b) { unsafe { raw(x) } } else { "" }`,
		`defer unsafe { release(b); }; return "";`,
		`return unsafe { let x = raw(b); x };`,
	} {
		source := "function f(b: u8[], ok: boolean): string { " + body + " }"
		prog, err := parser.Parse(source)
		if err != nil {
			t.Fatalf("%v\n%s", err, source)
		}
		for name, print := range map[string]func(*ast.Program) string{"format": Format, "print": Print} {
			out := print(prog)
			back, err := parser.Parse(out)
			if err != nil {
				t.Fatalf("%s: %v\n%s", name, err, out)
			}
			// Print does not write each statement kind. Where it writes the
			// block, it keeps the marker.
			if name == "print" && !strings.Contains(out, "(b)") && !strings.Contains(out, "(x)") {
				continue
			}
			if !strings.Contains(out, "unsafe {") {
				t.Errorf("%s lost the unsafe marker of %q:\n%s", name, body, out)
			}
			if name == "format" && ASTShape(prog) != ASTShape(back) {
				t.Errorf("format changed the syntax of %q: %s\n%s", body, FirstShapeDiff(ASTShape(prog), ASTShape(back)), out)
			}
			if name == "format" && Format(back) != out {
				t.Errorf("the formatter is not idempotent for %q:\n%s", body, out)
			}
		}
	}
}
