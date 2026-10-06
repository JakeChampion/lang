package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// A pair-form match payload handed to a callee whose parameter is
// owned-by-default is retained at the call for the callee's exit release,
// so the payload's own count stays the arm's: the arm ends with its drop.
// Before ownedCallArgRetained the occurrence was unexcused and the arm
// withheld the release, so the callee's dec took the retained count back to
// one and nobody took the last — one record per match leaked.
func TestPairFormPayloadHandedToOwningCalleeIsReleased(t *testing.T) {
	src := `struct S { data: u8[] }
struct R { path: string, body: S }
function mkr(n: i32): Option[R] { return Some(R { path: "abc", body: S { data: __alloc_u8(n) } }); }
function chk(n: i32): Option[string] { return Some("abc"); }
@noinline function bs(r: R): Result[string, i32] {
    match (chk(r.body.data.len())) { Some(s) => { return Ok(s); }, None => { return Err(1); } }
    return Ok(r.path);
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 10) {
        match (mkr(i + 3)) {
            Some(r) => { match (bs(r)) { Ok(s) => { t = t + s.len(); }, Err(e) => { t = t + 100; } } },
            None => { return 1; }
        }
        i = i + 1;
    }
    return t;
}`
	prevFree := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prevFree }()
	p := lowerSourceWith(t, src, 8)
	if !p.PairForm["mkr"] {
		t.Fatalf("mkr is not pair-form; the shape under test needs the register pair:\n%s", p)
	}
	fn := findFunc(p, "main")
	// The shape depends on bs owning r: the call site retains it. Without
	// the retain the test would be exercising the borrowed-callee rule.
	retained := false
	for i, op := range fn.Ops {
		if op.Kind == OpCallDirect && op.Str == "bs" && i > 0 && fn.Ops[i-1].Kind == OpRcInc {
			retained = true
		}
	}
	if !retained {
		t.Fatalf("bs does not own r (no retain before the call); the shape under test is gone:\n%s", p)
	}
	// The arm's release of r is a __drop_struct_R; the boxes main frees
	// through other paths (the loop's own temporaries) do not stand in
	// for it, so the count is pinned on the drop itself.
	if n := countCallDirect(fn.Ops, "__drop_struct_R"); n != 1 {
		t.Errorf("main drops R %d times, want 1: the pair-form payload handed to bs's owned parameter is released by nobody; ops:\n%s", n, p)
	}
}
