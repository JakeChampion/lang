package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// For-in element borrow (#6888): the desugar's per-iteration element binding
// `var sd = __foreach_iter_N[idx]` borrows when every use is a read through
// the value — no retain on bind, no per-iteration deep drop, no exit-sweep
// dec. These tests pin the borrow at the IR layer, guard by guard: the happy
// path costs the same rc traffic as the hand-written index spelling, and each
// escape shape keeps the owned model.

const forinScanPrelude = `struct S { name: string, fields: string[] }
function mk(n: string): S { return S{ name: n, fields: [n] }; }
function mks(): S[] { return [mk("a"), mk("bb")]; }
`

func rcTraffic(ip *ir.Program, fn string) (incs, decs, drops int) {
	f := funcByName(ip, fn)
	if f == nil {
		return -1, -1, -1
	}
	for _, op := range f.Ops {
		switch {
		case op.Kind == ir.OpRcInc:
			incs++
		case op.Kind == ir.OpRcDec:
			decs++
		case op.Kind == ir.OpCallDirect && strings.Contains(op.Str, "__drop_"):
			drops++
		}
	}
	return
}

// The read-only scanner: the for-in spelling must emit exactly the rc traffic
// of the index spelling — the element binding borrows, so its retain, its
// per-iteration deep drop, and its exit-sweep dec all vanish.
func TestForinElemBorrowMatchesIndexSpelling(t *testing.T) {
	ip := lowerForTest(t, forinScanPrelude+`
function scan_forin(xs: S[]): i32 {
    var n: i32 = 0;
    for sd in xs { n = n + sd.fields.len() + sd.name.len(); }
    return n;
}
function scan_index(xs: S[]): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { n = n + xs[i].fields.len() + xs[i].name.len(); i = i + 1; }
    return n;
}
function main(): i32 { return scan_forin(mks()) + scan_index(mks()); }`)
	fi, fd, fdr := rcTraffic(ip, "scan_forin")
	ii, id, idr := rcTraffic(ip, "scan_index")
	// The for-in spelling keeps exactly one extra inc/dec pair: the synthetic
	// iterand local retains the param container once per LOOP. The element
	// itself must be free — no per-iteration retain, no drop calls at all.
	if fi != ii+1 || fd != id+1 || fdr != idr {
		t.Errorf("for-in scanner should cost the index spelling plus one iterand pair: forin inc=%d dec=%d drops=%d, index inc=%d dec=%d drops=%d", fi, fd, fdr, ii, id, idr)
	}
	if fdr != 0 {
		t.Errorf("read-only for-in scanner should emit no drop calls, got %d", fdr)
	}
}

// The other two iterand classes: a LOCAL container (the synthetic iter chains
// through the walk-2 borrowed alias of the local) and a CALL-RESULT container
// (the iter owns the move). In both, the element binding borrows, so neither
// function carries a single retain: xs arrives by move, iter by borrow or
// move, the element by borrow.
func TestForinElemBorrowLocalAndCallIterands(t *testing.T) {
	ip := lowerForTest(t, forinScanPrelude+`
function scan_local(): i32 {
    var xs: S[] = mks();
    var n: i32 = 0;
    for sd in xs { n = n + sd.fields.len(); }
    return n;
}
function scan_call(): i32 {
    var n: i32 = 0;
    for sd in mks() { n = n + sd.fields.len(); }
    return n;
}
function main(): i32 { return scan_local() + scan_call(); }`)
	for _, fn := range []string{"scan_local", "scan_call"} {
		incs, _, _ := rcTraffic(ip, fn)
		if incs != 0 {
			t.Errorf("%s: move-in container + borrowed element should need no retains, got %d rc_inc", fn, incs)
		}
	}
}

// The user-callee leg of bindingConfinedToArm delegates to the paramEscapes
// oracle (borrowingCallArg): a callee whose parameter provably does not escape
// accepts the borrow, one that stores its parameter refuses it. This pins both
// directions of that delegation through the for-in path.
func TestForinElemBorrowUserCalleeEscape(t *testing.T) {
	ip := lowerForTest(t, forinScanPrelude+`
function note(s: S): i32 { return s.fields.len(); }
function stash(acc: S[], s: S): S[] { return acc.append(s); }
function scan_note(xs: S[]): i32 {
    var n: i32 = 0;
    for sd in xs { n = n + note(sd); }
    return n;
}
function scan_stash(xs: S[]): i32 {
    var acc: S[] = [];
    for sd in xs { acc = stash(acc, sd); }
    return acc.len();
}
function main(): i32 { return scan_note(mks()) + scan_stash(mks()); }`)
	ni, _, ndr := rcTraffic(ip, "scan_note")
	if ni != 1 || ndr != 0 {
		t.Errorf("non-escaping user callee should accept the borrow (only the iterand pair): inc=%d drops=%d", ni, ndr)
	}
	si, _, _ := rcTraffic(ip, "scan_stash")
	if si <= 1 {
		t.Errorf("escaping user callee should keep the owned element: inc=%d", si)
	}
}

// The nested scanner (the decl_field_type shape from #6888): the outer element
// borrows, the inner iterand is an owned field alias retained once per OUTER
// iteration, and the inner element borrows from it — so the whole nest emits
// no drop calls and exactly two retains (outer iterand + inner iterand).
func TestForinElemBorrowNested(t *testing.T) {
	ip := lowerForTest(t, forinScanPrelude+`
function scan_nested(xs: S[]): i32 {
    var n: i32 = 0;
    for sd in xs {
        for f in sd.fields { n = n + f.len(); }
    }
    return n;
}
function main(): i32 { return scan_nested(mks()); }`)
	incs, _, drops := rcTraffic(ip, "scan_nested")
	if drops != 0 {
		t.Errorf("read-only nested for-in should emit no drop calls, got %d", drops)
	}
	if incs > 2 {
		t.Errorf("nested for-in should retain only the two iterands, got %d rc_inc", incs)
	}
}

// Each escaping shape must refuse the borrow: the element keeps its retain,
// so the function carries strictly more rc_inc than its confined sibling.
// The confined sibling in every pair is scan_ok, byte-for-byte the happy path.
//
// These are the failing-mode net for the walk-3 guards — the e2e corpus
// cannot be: measured with every guard knocked out, the rcCorpus escape
// programs still exit 0 on all three backends (escape sites take their own
// transfer inc, and the one uncounted route, move-on-return, is absorbed as
// a LEAK by the caller's may-alias-result flat dec). Per-case coverage,
// also measured by knockout: returned falls to the returned[y]+confinement
// pair, bound_alias to walk 2's role marking, stored_into_array to
// movedLocals, match_scrutinee to scrutinee[y], reassigned_elem to
// reassigned[y].
func TestForinElemBorrowRefusesEscapes(t *testing.T) {
	cases := []struct {
		name string
		fn   string
	}{
		{"bound_alias", `
function esc(xs: S[]): i32 {
    var n: i32 = 0;
    for sd in xs { var keep: S = sd; n = n + keep.fields.len(); }
    return n;
}`},
		{"returned", `
function esc(xs: S[]): S {
    for sd in xs { return sd; }
    return mk("z");
}`},
		{"stored_into_array", `
function esc(xs: S[]): i32 {
    var acc: S[] = [];
    for sd in xs { acc = acc.append(sd); }
    return acc.len();
}`},
		{"match_scrutinee", `
enum E { Has(S), No }
function wrap(s: S): E { return Has(s); }
function esc(xs: S[]): i32 {
    var n: i32 = 0;
    for sd in xs { match (sd) { _ => { n = n + 1; } } }
    return n;
}`},
		{"reassigned_elem", `
function esc(xs: S[]): i32 {
    var n: i32 = 0;
    for sd in xs { sd = mk("q"); n = n + sd.fields.len(); }
    return n;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := lowerForTest(t, forinScanPrelude+tc.fn+`
function scan_ok(xs: S[]): i32 {
    var n: i32 = 0;
    for sd in xs { n = n + sd.fields.len() + sd.name.len(); }
    return n;
}
function main(): i32 { return scan_ok(mks()); }`)
			oi, _, _ := rcTraffic(ip, "scan_ok")
			ei, _, _ := rcTraffic(ip, "esc")
			if ei <= oi {
				t.Errorf("escaping element should keep its retain: esc inc=%d, confined scan inc=%d", ei, oi)
			}
		})
	}
}
