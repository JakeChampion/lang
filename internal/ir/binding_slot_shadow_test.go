package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// A match-arm BINDING that shares its name with a `var`-declared local in
// sibling arms must REUSE that name's slot, not allocate a fresh one.
//
// The return / exit dec sweep resolves names through the builder's slot map
// at the moment each return is lowered; a fresh binding slot permanently
// shadows the var's pre-allocated (entry-zeroed) slot, so every
// later-lowered return sweeps the ARM's slot — which is never written on
// paths that don't enter that arm. The sweep then rc_dec's uninitialized
// stack garbage; when the leftover value looks like a heap pointer it
// decrements a random live block's rc — a layout-dependent heap corruption.
// Observed in the wild as the self-host driver miscompiling
// `match(read_file(..)) { Ok(s) => { write(s); .. } }` (a dangling
// .Lir_main_* branch label): irlower's alias_names_in_stmt binds its
// StmtAssign arm payload as `a`, shadowing the `var a: string[]`
// accumulators declared in sibling arms (TestSelfHostReadFileIRX86_64/echo
// pins it end to end).
//
// The pin: every OpLoadLocal feeding an rc_dec must target a slot that is
// either a parameter slot or one that some OpStoreLocal / entry
// zero-store DOMINATING-ly writes — approximated here by asserting the
// wildcard arm's return decs the SAME slot the entry zero-store writes
// (the shared, entry-zeroed `a` slot), not a fresh arm-local slot.
func TestMatchBindingReusesShadowedVarSlot(t *testing.T) {
	// Mirrors alias_names_in_stmt's shape: arm 2 binds its payload as `a`
	// (the same name the StmtIf-like arm declares with `var a`), and the
	// wildcard arm returns straight through the exit sweep.
	ip := lowerForTest(t, `
struct SV { init: i32 }
struct SA { value: i32 }
struct SI { n: i32 }
struct SE { n: i32 }
type St = SV | SA | SI | SE;

function idsin(v: i32, acc: string[]): string[] { return acc; }

function walk(st: St, acc: string[]): string[] {
    match (st) {
        SV(v) => { return idsin(v.init, acc); },
        SA(a) => { return idsin(a.value, acc); },
        SI(iff) => {
            var a: string[] = idsin(iff.n, acc);
            return idsin(iff.n, a);
        },
        _ => { return acc; },
    }
}
function main(): i32 {
    var st: St = SE { n: 1 };
    return walk(st, []).len();
}`)
	fn := funcByName(ip, "walk")
	if fn == nil {
		t.Fatal("walk not lowered")
	}
	// Zero-stored slots: every `const.i32 0; local.store N` target. This
	// covers the entry safety net for rc-tracked locals, the pre-zeroed
	// consuming-match binding slots (#4400), and the per-arm "param is
	// dead" stamps — all of which make their slot safe to sweep.
	zeroed := map[int32]bool{}
	for i := 0; i+1 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == ir.OpConstI32 && fn.Ops[i].I32 == 0 &&
			fn.Ops[i+1].Kind == ir.OpStoreLocal {
			zeroed[fn.Ops[i+1].I32] = true
		}
	}
	if len(zeroed) == 0 {
		t.Fatal("no zero-store found (safety-net layout changed? update the test)")
	}
	// Every `local.load N; call __fern_rc_dec` sweep must target either a
	// parameter slot (0..1 — the owned-by-default param dec, always
	// initialized) or a zero-stored slot. Without the reuse fix, the SA
	// arm's fresh binding slot shadows `a`, and the sweeps lowered after
	// that arm (the SI arm's and the wildcard's returns) dec THAT slot —
	// unwritten (and never zero-inited) on every path that skips the arm.
	for i := 0; i+1 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == ir.OpLoadLocal &&
			fn.Ops[i+1].Kind == ir.OpRcDec {
			slot := fn.Ops[i].I32
			if slot > 1 && !zeroed[slot] {
				t.Errorf("return sweep decs slot %d (not a param, not zero-stored) — a same-named match binding shadowed the var's slot; that slot is uninitialized stack garbage on paths that skip its arm (heap corruption)", slot)
			}
		}
	}
}

// The cross-shape counterpart: when a match-arm binding shares its name
// with a `var` whose slot has a DIFFERENT physical shape — here a
// two-word string `var t` (any two-word ABI: wasm ptrW==4, arm64
// TwoWordOverride) colliding with a pointer-shaped binding
// `ST(t)` — the binding must get a FRESH slot, not reuse the var's.
//
// bindingSlot's shape guard used to read the existing slot's type from
// b.scratchType, which is never stamped for `var`-declared (info.Locals)
// slots — nil read as "single-word", the guard passed, and the binding
// reused the string's two-word slot. The backend sizes physical slots
// from the declared local type, so every OpLoadLocal / OpStoreLocal of
// the binding fanned into two words while the IR balanced the operand
// stack for one: the store popped a garbage second word and each load
// pushed one, desynchronising every stack-machine backend. Observed in
// the wild as the self-host interp's `parser.ExprTuple(t)` arm trapping
// its own bounds check on arm64 (TestSelfHostInterpArm64, exit 134)
// once a sibling arm gained `var t: string` (#4497).
func TestMatchBindingCrossShapeVarCollisionGetsFreshSlot(t *testing.T) {
	src := `
struct SN { text: string }
struct ST { elements: i32[] }
type E = SN | ST;

function eval(e: E): i32 {
    match (e) {
        SN(n) => {
            var t: string = n.text;
            return t.len();
        },
        ST(t) => {
            var s: i32 = 0;
            var i: i32 = 0;
            while (i < t.elements.len()) {
                s = s + t.elements[i];
                i = i + 1;
            }
            return s;
        }
    }
    return 0 - 1;
}
function main(): i32 {
    var e: E = ST { elements: [40, 2] };
    return eval(e);
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// ptrW==4 (the wasm shape) has two-word strings unconditionally —
	// the same slot-shape split the arm64 backend opts into via
	// ast.TwoWordOverride.
	ip, err := ir.LowerWith(prog, info, 4)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	fn := funcByName(ip, "eval")
	if fn == nil {
		t.Fatal("eval not lowered")
	}
	// The string var's slot is the one the entry safety net zeroes with
	// TWO zero-pushes (a two-word store); it's the only rc-tracked local
	// in `eval`, so the pattern is unique.
	strSlot := int32(-1)
	for i := 0; i+2 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == ir.OpConstI32 && fn.Ops[i].I32 == 0 &&
			fn.Ops[i+1].Kind == ir.OpConstI32 && fn.Ops[i+1].I32 == 0 &&
			fn.Ops[i+2].Kind == ir.OpStoreLocal {
			strSlot = fn.Ops[i+2].I32
			break
		}
	}
	if strSlot < 0 {
		t.Fatal("no two-word entry zero-store found (safety-net layout changed? update the test)")
	}
	// Exactly two stores may target the string slot: the entry zero-init
	// and the SN arm's `var t = n.text`. A third store is the ST arm's
	// binding wrongly reusing the two-word slot for its one-word payload.
	stores := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpStoreLocal && op.I32 == strSlot {
			stores++
		}
	}
	if stores != 2 {
		t.Errorf("string var slot %d has %d stores, want 2 (entry zero-init + var init); a cross-shape match binding is sharing the two-word slot", strSlot, stores)
	}
}

// The cross-WIDTH counterpart of the cross-shape test above: same-named
// match-arm bindings whose payload types map to DIFFERENT wasm valtypes
// — `I(v)` (i32) vs `L(v)` (i64) — must not share a slot. The backend
// types the physical wasm local from the slot's final type stamp, so a
// shared slot leaves one arm's store/load ill-typed and the emitted
// module fails validation ("type mismatch: expected i64, found i32") —
// the TestExternVariant{MixedWidth,NonUniform}ResultCustomProvider
// composition shape, which only runs where wasmtime/wasm-tools are
// installed; this pin gates it in the unit lane. bindingSlot's shape
// guard used to compare only two-word-ness, under which i32 and i64 both
// read "one word" and shared.
func TestMatchBindingCrossWidthGetsDistinctSlots(t *testing.T) {
	src := `
enum V { I(i32), L(i64) }

function mk(n: i32): V {
    if (n < 100) { return I(n); }
    return L(5000000000);
}

function pick(n: i32): i32 {
    match (mk(n)) {
        I(v) => { return v; },
        L(v) => { if (v == 5000000000) { return 42; } return -1; },
    }
}
function main(): i32 {
    if (pick(5) == 5 && pick(200) == 42) { return 0; }
    return 1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// ptrW==4: the wasm shape, where locals are valtype-checked.
	ip, err := ir.LowerWith(prog, info, 4)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	fn := funcByName(ip, "pick")
	if fn == nil {
		t.Fatal("pick not lowered")
	}
	// Collect the store-width sets per slot: every OpLoad directly
	// feeding an OpStoreLocal / OpTeeLocal is a payload extraction, and
	// Op.Width says which arm it belongs to (0 = i32 default, 64 = i64).
	widths := map[int32]map[int]bool{}
	for i := 0; i+1 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind != ir.OpLoad {
			continue
		}
		next := fn.Ops[i+1]
		if next.Kind != ir.OpStoreLocal && next.Kind != ir.OpTeeLocal {
			continue
		}
		if widths[next.I32] == nil {
			widths[next.I32] = map[int]bool{}
		}
		widths[next.I32][fn.Ops[i].Width] = true
	}
	sawI64 := false
	for slot, ws := range widths {
		if len(ws) > 1 {
			t.Errorf("slot %d receives loads of multiple widths %v — cross-width same-named bindings are sharing a slot (invalid wasm local typing)", slot, ws)
		}
		if ws[64] {
			sawI64 = true
		}
	}
	if !sawI64 {
		t.Fatal("no i64 payload extraction found (lowering shape changed? update the test)")
	}
}
