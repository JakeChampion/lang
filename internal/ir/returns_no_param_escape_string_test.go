package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// exprNoParamEscape string-freshness cases (#4355): a constructor embedding a
// string LITERAL or CONCAT into a returned struct/enum keeps its
// returnsNoParamEscape verdict — a literal is a static sentinel and a concat
// byte-copies into a fresh buffer, so neither can carry a parameter's heap.
// Before the fix, such constructors lost the verdict, rhsTainted's generic
// any-arg-tainted rule poisoned every local bound to their call result, and
// the whole chain (enum box + payload struct box + string) was never swept —
// pinned here by the presence of the __drop_enum_E reinit drop in main.
// A constructor embedding a PARAM string must NOT get the verdict (its
// result aliases caller heap) — pinned by the drop's absence.
func dropCallCountInFn(ip *ir.Program, fn, callee string) int {
	f := funcByName(ip, fn)
	n := 0
	for _, op := range f.Ops {
		if op.Kind == ir.OpCallDirect && strings.Contains(op.Str, callee) {
			n++
		}
	}
	return n
}

func TestReturnsNoParamEscapeStringFresh(t *testing.T) {
	// Literal string field: mk is escape-free, the loop local is swept via
	// __drop_enum_E.
	const lit = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(n: i32): E { return A(S { name: "ab", n: n }, n); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var e: E = mk(i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    return acc;
}`
	if n := dropCallCountInFn(lowerForTest(t, lit), "main", "__drop_enum_E"); n == 0 {
		t.Errorf("literal string field: main emits no __drop_enum_E — string-embedding ctor lost returnsNoParamEscape")
	}

	// Concat string field (operand is a param — still fresh: concat copies).
	const concat = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var e: E = mk("a", i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    return acc;
}`
	if n := dropCallCountInFn(lowerForTest(t, concat), "main", "__drop_enum_E"); n == 0 {
		t.Errorf("concat string field: main emits no __drop_enum_E — concat should be provenance-free fresh")
	}

	// PARAM-embedding ctor: `S { name: nm }` aliases the caller's string, so
	// returnsNoParamEscape stays FALSE — but the alias is COUNTED (the StructLit
	// field inc), and inferParamCountedRetain reads exactly that: the caller's
	// local stays reclaimable and the result is swept. Both halves of the pair
	// are then balanced — the sweep decs the field, the caller's own release
	// decs the last reference — where the old conservative answer stranded the
	// enum box, the payload struct box AND the string on every call.
	const paramEmbed = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm, n: n }, n); }
function main(): i32 {
    var keep: string = "aa" + "bb";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var e: E = mk(keep, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    return acc + keep.len();
}`
	if n := dropCallCountInFn(lowerForTest(t, paramEmbed), "main", "__drop_enum_E"); n == 0 {
		t.Errorf("param-embedding ctor: main emits no __drop_enum_E — a COUNTED param alias must stay reclaimable (inferParamCountedRetain)")
	}

	// UNCOUNTED param retention: the callee pushes the string into an array it
	// returns. `xs.append(nm)` is not a counting construction site the summary
	// recognises (it is a call, not a literal), so the param is NOT counted-only
	// and the conservative taint stands — the caller must not reclaim `keep`.
	// This is the #4174 shape: a self-host codegen helper stored a string arg
	// into an array field of the returned struct and the caller-side str_dec
	// recycled its box mid-codegen.
	const uncounted = `struct S { names: string[], n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E {
    var xs: string[] = [];
    xs = xs.append(nm);
    return A(S { names: xs, n: n }, n);
}
function main(): i32 {
    var keep: string = "aa" + "bb";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var e: E = mk(keep, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    return acc + keep.len();
}`
	if n := dropCallCountInFn(lowerForTest(t, uncounted), "main", "__drop_enum_E"); n != 0 {
		t.Errorf("uncounted param retention: main emits %d __drop_enum_E calls, want 0 (the append is not a counted construction)", n)
	}
}
