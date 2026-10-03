package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A pair-form match payload passed to a callee that borrows the parameter
// and retains what it keeps is still the arm's to release (#10669): the
// arm ends with the payload's own drop, where before nothing released it.
func TestPairFormPayloadHandedToBorrowingCalleeIsReleased(t *testing.T) {
	src := `struct Bag { xs: string[] }
function keep(b: Bag, v: string): Bag { return Bag { xs: b.xs.append(v) }; }
function mk(n: i32): Option[string] {
    if (n < 0) { return None; }
    return Some("v" + "alue");
}
function main(): i32 {
    let b: Bag = Bag { xs: [] };
    let i: i32 = 0;
    while (i < 3) {
        match (mk(i)) {
            Some(v) => { b = keep(b, v); },
            None => { return 1; }
        }
        i = i + 1;
    }
    return b.xs.len() - 3;
}`
	prevFree := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prevFree }()
	p := lowerSourceWith(t, src, 8)
	fn := findFunc(p, "main")
	if !p.PairForm["mk"] {
		t.Fatalf("mk is not pair-form; the shape under test needs the register pair:\n%s", p)
	}
	if n := countCallDirect(fn.Ops, "__fern_str_dec"); n == 0 {
		t.Errorf("main releases no string: the pair-form payload handed to keep's borrowed parameter is released by nobody; ops:\n%s", p)
	}
}
