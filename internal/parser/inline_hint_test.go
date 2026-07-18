package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// `@inline` / `@noinline` parse as function attributes and stamp
// FuncDecl.InlineHint (#4412 Rec §14).
func TestInlineHintAttributes(t *testing.T) {
	prog, err := Parse(`@inline
function fast(x: i32): i32 { return x; }
@noinline
pub function slow(x: i32): i32 { return x; }
function plain(): i32 { return 0; }
function main(): i32 { return fast(1) + slow(2) + plain(); }`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ast.InlineHint{
		"fast":  ast.InlineHintAlways,
		"slow":  ast.InlineHintNever,
		"plain": ast.InlineHintNone,
		"main":  ast.InlineHintNone,
	}
	for _, fn := range prog.Funcs {
		if w, ok := want[fn.Name]; ok && fn.InlineHint != w {
			t.Errorf("%s: InlineHint = %d, want %d", fn.Name, fn.InlineHint, w)
		}
	}
}

// The attributes only apply to functions — a struct target is a parse
// error, mirroring @must_consume's placement rule.
func TestInlineHintOnStructRejected(t *testing.T) {
	_, err := Parse(`@inline
struct P { x: i32 }
function main(): i32 { return 0; }`)
	if err == nil {
		t.Fatal("@inline on a struct should be a parse error")
	}
	if !strings.Contains(err.Error(), "only applies to a function") {
		t.Errorf("error should name the placement rule, got: %v", err)
	}
}
