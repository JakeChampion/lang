package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #7910 (d) — a NESTED enum payload consumed straight off a call.
//
// `match (mk(i)) { Some(o) => { match (o) { Some(xs) => … } } … }` binds `o`,
// a pointer to the inner box, and reads it through a second match. The fresh
// scrutinee reclaim (reclaimableMatchScrutinee) admits a pointer binding only
// when it is confined to its arm, and bindingConfinedToArm's excuse list —
// field access, index, borrowing call argument — had no entry for "the
// scrutinee of a nested match", so `o` read as an escape and the outer box
// was never released: outer box, inner box and payload all stranded, one set
// per call. Every single-level position (Ok / Err / Some, string / string[] /
// i32[]) was already clean, and so was the same nested value once BOUND to a
// local first; the leak was the direct-call position alone.
//
// A nested match whose arms bind no `@`, no sub-pattern, and confine their
// own pointer bindings is now excused, so the join's deep drop reaches the
// inner box through the generated __drop_enum_ for the instantiation.

// The four nested positions the isolation table found leaking, each read
// through a nested match statement with the payload confined to its arm.
const nestedEnumScrutineeSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
enum In { S(string[]), N }
function mk_oo(i: i32): Option[Option[string[]]] { if (i % 3 == 0) { return None; } return Some(Some([w(i)])); }
function mk_os(i: i32): Option[Option[string]] { return Some(Some(w(i))); }
function mk_oi(i: i32): Option[Option[i32]] { return Some(Some(i)); }
function mk_oe(i: i32): Option[In] { if (i % 2 == 0) { return Some(In.N); } return Some(In.S([w(i)])); }
function mk_ro(i: i32): Result[Option[string[]], string] { if (i % 2 == 0) { return Err(w(i)); } return Ok(Some([w(i)])); }
function round(i: i32): i32 {
    var t: i32 = 0;
    match (mk_oo(i)) { Some(o) => { match (o) { Some(xs) => { t = t + xs.len(); }, None => { t = t + 1; } } }, None => { t = t + 2; } }
    match (mk_os(i)) { Some(o) => { match (o) { Some(s) => { t = t + s.len(); }, None => { t = t + 1; } } }, None => { t = t + 2; } }
    match (mk_oi(i)) { Some(o) => { match (o) { Some(x) => { t = t + x; }, None => { t = t + 1; } } }, None => { t = t + 2; } }
    match (mk_oe(i)) { Some(o) => { match (o) { In.S(xs) => { t = t + xs.len(); }, In.N => { t = t + 1; } } }, None => { t = t + 2; } }
    match (mk_ro(i)) { Ok(o) => { match (o) { Some(xs) => { t = t + xs.len(); }, None => { t = t + 1; } } }, Err(e) => { t = t + e.len(); } }
    return t;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (acc < 0) { return 1; }
    return 0;
}`

func nestedEnumScrutineeBumpSrc(n string) string {
	return `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function mk_oo(i: i32): Option[Option[string[]]] { if (i % 3 == 0) { return None; } return Some(Some([w(i)])); }
function mk_ro(i: i32): Result[Option[string[]], string] { if (i % 2 == 0) { return Err(w(i)); } return Ok(Some([w(i)])); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        match (mk_oo(i)) { Some(o) => { match (o) { Some(xs) => { t = t + xs.len(); }, None => { t = t + 1; } } }, None => { t = t + 2; } }
        match (mk_ro(i)) { Ok(o) => { match (o) { Some(xs) => { t = t + xs.len(); }, None => { t = t + 1; } } }, Err(e) => { t = t + e.len(); } }
        i = i + 1;
    }
    if (t < 0) { return t; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Value correctness + zero over-release on the same positions: the inner
// payload must still read right inside the arm, and the join's deep drop must
// release each box exactly once — the direction leakcheck cannot see. The
// inner match binding is read through a method AND indexed, so a premature
// free of the inner box or its array would change the answer.
const nestedEnumScrutineeUnderflowSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function mk_oo(i: i32): Option[Option[string[]]] { if (i % 3 == 0) { return None; } return Some(Some([w(i), w(i + 1)])); }
function mk_ro(i: i32): Result[Option[string[]], string] { if (i % 2 == 0) { return Err(w(i)); } return Ok(Some([w(i)])); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        match (mk_oo(i)) { Some(o) => { match (o) { Some(xs) => { acc = acc + xs.len() + xs[1].len(); }, None => { acc = acc + 1000; } } }, None => { acc = acc + 1; } }
        match (mk_ro(i)) { Ok(o) => { match (o) { Some(xs) => { acc = acc + xs[0].len(); }, None => { acc = acc + 1000; } } }, Err(e) => { acc = acc + e.len(); } }
        i = i + 1;
    }
    // w(even) is 45 chars, w(odd) 44. mk_oo: the 100 None rounds (i % 3 == 0)
    // add 1; the 200 Some rounds add 2 + len(w(i + 1)), with i + 1 even on the
    // 100 odd non-multiples of 3 and odd on the 100 even ones. mk_ro: 150 Err
    // rounds (even i) add len(w(i)), 150 Ok rounds (odd i) add len(w(i)).
    var want: i32 = 100 + 200 * 2 + 100 * 45 + 100 * 44 + 150 * 45 + 150 * 44;
    if (acc != want) { return 99; }
    return __rc_underflow_count();
}`

func TestX86_64NestedEnumScrutineeReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, nestedEnumScrutineeSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (nested boxes and wide strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("nested enum scrutinee leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunX86_64FreeOn(t, nestedEnumScrutineeUnderflowSrc); code != 0 {
		t.Errorf("nested enum scrutinee reclaim: code=%d (99=wrong value, >0=over-release)", code)
	}
	small := mustRunX86_64FreeOn(t, nestedEnumScrutineeBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, nestedEnumScrutineeBumpSrc("5000"))
	if small != large {
		t.Errorf("nested scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64NestedEnumScrutineeReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, nestedEnumScrutineeSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (nested boxes and wide strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("nested enum scrutinee leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunArm64FreeOn(t, nestedEnumScrutineeUnderflowSrc); code != 0 {
		t.Errorf("nested enum scrutinee reclaim: code=%d (99=wrong value, >0=over-release)", code)
	}
	small := mustRunArm64FreeOn(t, nestedEnumScrutineeBumpSrc("50"))
	large := mustRunArm64FreeOn(t, nestedEnumScrutineeBumpSrc("5000"))
	if small != large {
		t.Errorf("nested scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWASMNestedEnumScrutineeReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, nestedEnumScrutineeBumpSrc("50"))
	large := runWasm(t, nestedEnumScrutineeBumpSrc("5000"))
	if small != large {
		t.Errorf("nested scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, nestedEnumScrutineeUnderflowSrc); got != 0 {
		t.Errorf("nested enum scrutinee reclaim: code=%d (99=wrong value, >0=over-release)", got)
	}
}
