package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A fresh array handed to an array-returning callee is released after the
// call only when every return is param-free or a bare parameter the Return
// lowering hands back with the transfer inc (#10140). The shapes refused here
// return something that names an argument without that count, or rewrite the
// argument's buffer before handing it back.
const returnedArrayParamsSrc = `function first(x: i32[]): i32[] { return x; }
function or_default(x: i32[], d: boolean): i32[] {
    if (d) { return [9]; }
    return x;
}
function pick(a: i32[], b: i32[], c: boolean): i32[] {
    if (c) { return a; }
    return b;
}
function grow(x: i32[]): i32[] { return x.append(7); }
function thread(x: i32[]): i32[] {
    x = x.append(5);
    return x;
}
function grow_other(x: i32[], y: i32[]): i32[] {
    var z: i32[] = x.append(1);
    if (z.len() > 9) { return [0]; }
    return y;
}
function maybe_reset(x: i32[], c: boolean): i32[] {
    if (c) { x = [1]; }
    return x;
}
function head(xss: i32[][]): i32[] { return xss[0]; }
struct S { xs: i32[] }
function peel(s: S): i32[] { return s.xs; }
function via(x: i32[]): i32[] {
    var y: i32[] = x;
    return y;
}
function main(): i32 { return 0; }`

func TestFreshArrayArgReleasedOnlyThroughACountedReturn(t *testing.T) {
	prog, err := parser.Parse(returnedArrayParamsSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	noEscape := findReturnsNoParamEscape(prog, info)
	b := &builder{
		returnsNoParamEscape: noEscape,
		returnedArrayParams:  findReturnedArrayParams(prog, info, noEscape),
		consumedArrayArgPos:  consumedArrayParamPositions(prog, info, nil),
		growParams:           computeGrowParams(prog, info, computeParamFieldObs(prog, nil)),
		trmcFuncs:            map[string]bool{},
	}
	arr := ast.ArrayType{Elem: ast.NumberType{Width: 32, Signed: true}}
	for fn, want := range map[string]bool{
		"first":       true,
		"or_default":  true,
		"pick":        true,
		"grow":        false, // may reallocate the argument's buffer in place
		"thread":      false, // consumed-threaded: the flag protocol hands it back uncounted
		"grow_other":  false, // grows x in place, which the post-call dec of x would then read
		"maybe_reset": false, // consumed-threaded without growing: returned bare, uncounted
		"head":        false, // an element of the argument, not the argument
		"peel":        false, // a field of the argument
		"via":         false, // an alias of the parameter
	} {
		if got := b.resultIsCountedParamAlias(fn, arr); got != want {
			t.Errorf("resultIsCountedParamAlias(%s) = %v, want %v", fn, got, want)
		}
	}
}
