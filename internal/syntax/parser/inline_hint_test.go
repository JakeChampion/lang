package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

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

func TestInlineHintOnImplMethods(t *testing.T) {
	prog, err := Parse(`trait Pick { function a(self: Self): i32; function b(self: Self): i32; function c(self: Self): i32; }
impl Pick for f64x2 {
  @inline
  function a(self: f64x2): i32 { return 1; }
  @noinline
  function b(self: f64x2): i32 { return 2; }
  function c(self: f64x2): i32 { return 3; }
}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []ast.InlineHint{ast.InlineHintAlways, ast.InlineHintNever, ast.InlineHintNone}
	for i, m := range prog.Impls[0].Methods {
		if m.InlineHint != want[i] {
			t.Errorf("%s: InlineHint = %d, want %d", m.Name, m.InlineHint, want[i])
		}
	}
}

func TestOtherAttributeOnImplMethodRejected(t *testing.T) {
	_, err := Parse(`trait Pick { function a(self: Self): i32; }
impl Pick for f64x2 {
  @must_consume
  function a(self: f64x2): i32 { return 1; }
}`)
	if err == nil || !strings.Contains(err.Error(), "an impl method takes only @inline or @noinline") {
		t.Errorf("want the impl method attribute rule, got: %v", err)
	}
}
