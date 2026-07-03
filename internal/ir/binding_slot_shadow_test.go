package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
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
	// Entry zero-store slot: the first `const.i32 0; local.store N` pair.
	zeroSlot := int32(-1)
	for i := 0; i+1 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == ir.OpConstI32 && fn.Ops[i].I32 == 0 &&
			fn.Ops[i+1].Kind == ir.OpStoreLocal {
			zeroSlot = fn.Ops[i+1].I32
			break
		}
	}
	if zeroSlot < 0 {
		t.Fatal("no entry zero-store found (safety-net layout changed? update the test)")
	}
	// Every `local.load N; call __fern_rc_dec` sweep must target either a
	// parameter slot (0..1 — the owned-by-default param dec, always
	// initialized) or the entry-zeroed shared `a` slot. Without the reuse
	// fix, the SA arm's fresh binding slot shadows `a`, and the sweeps
	// lowered after that arm (the SI arm's and the wildcard's returns)
	// dec THAT slot — unwritten on every path that skips the SA arm.
	for i := 0; i+1 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == ir.OpLoadLocal &&
			fn.Ops[i+1].Kind == ir.OpCallDirect && fn.Ops[i+1].Str == "__fern_rc_dec" {
			slot := fn.Ops[i].I32
			if slot > 1 && slot != zeroSlot {
				t.Errorf("return sweep decs slot %d (not a param, not the entry-zeroed shared slot %d) — a same-named match binding shadowed the var's slot; that slot is uninitialized stack garbage on paths that skip its arm (heap corruption)", slot, zeroSlot)
			}
		}
	}
}
