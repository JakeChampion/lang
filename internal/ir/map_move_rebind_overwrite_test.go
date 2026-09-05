package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// mapAliasedOverwriteDecCount counts map overwrite sites that release the old
// handle on the SAME-POINTER arm.
//
// The site emits `if (old != new) { <map slot drop> } else { dec(old) }`, so the
// dec is found by locating each pointer-width OpNe / OpIf pair and walking to
// the OpElse that closes that if — a bare OpRcDec count cannot do it, because
// the != arm's slot drop and the binding-site retains emit their own.
func mapAliasedOverwriteDecCount(fn *ir.Func) int {
	n := 0
	for i := 0; i+1 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind != ir.OpNe || fn.Ops[i].Width != ir.WidthPtr || fn.Ops[i+1].Kind != ir.OpIf {
			continue
		}
		depth := 0
		for j := i + 1; j < len(fn.Ops); j++ {
			switch fn.Ops[j].Kind {
			case ir.OpIf, ir.OpBlock, ir.OpLoop:
				depth++
			case ir.OpEnd:
				depth--
			case ir.OpElse:
				if depth != 1 {
					continue
				}
				if j+2 < len(fn.Ops) && fn.Ops[j+1].Kind == ir.OpLoadLocal && fn.Ops[j+2].Kind == ir.OpRcDec {
					n++
				}
			}
			if depth == 0 {
				break
			}
		}
	}
	return n
}

// `var (m2, ok) = m.without(k); m = m2` — the destructured handle rebound onto
// the local it came from. __map_cow_inplace hands the receiver straight back on
// its in-place branch, so `m2` and the slot `m` is about to overwrite are the
// SAME pointer, and the same-pointer arm is the only place that release can go.
//
// The alias is a MOVE: it skips its transfer inc and skips m2's exit sweep. It
// still hands the slot a count — m2's — on top of the one the slot already had,
// so the release is owed exactly as it is for a retained alias. Gating on "was
// an inc emitted" instead stranded the whole table once per rebind: 112000 B
// over the corpus fixture's 500 rounds on x86-64, 128000 on arm64, 96000 on
// wasm32, all now zero (#8434).
func TestMapMoveRebindReleasesTheOverwrittenHandle(t *testing.T) {
	const src = `
import "core/int";
import "core/map";
import "std/string";
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var sm: Map[string, i32] = map_new(8);
    sm = sm.insert("ke" + "y", n);
    sm = sm.insert("ot" + "her", 3);
    var (m2, ok) = sm.without("ke" + "y");
    sm = m2;
    if (ok) { acc = acc + 2; }
    return acc + sm.get_or("ot" + "her", 0);
}

function main(): i32 { return churn(4); }
`
	fn := funcByName(lowerForTest(t, src), "churn")
	if n := mapAliasedOverwriteDecCount(fn); n == 0 {
		t.Error("`sm = m2` emits no same-pointer release: the moved alias's count is stranded and the COW in-place table leaks once per rebind (#8434)")
	}
}

// The control that keeps the release honest. A rebind from a FRESH producer
// that never mentions the local hands the slot a reference nobody else counts,
// so the same-pointer arm must not exist at all — the pointers cannot be equal,
// and a dec emitted there would be an over-release rather than dead code. This
// is the half `isSelfMapMutation`'s COW-aware branch was written to protect, so
// widening the release must not reach it.
func TestFreshMapProducerRebindEmitsNoAliasedRelease(t *testing.T) {
	const src = `
import "core/int";
import "core/map";

function mk(seed: i32): Map[i32, i32] {
    var fresh: Map[i32, i32] = map_new(8);
    return fresh.insert(seed, seed + 1);
}

function churn(n: i32): i32 {
    var acc: i32 = 0;
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 4);
    m = mk(n);
    return acc + m.get_or(n, 0);
}

function main(): i32 { return churn(4); }
`
	fn := funcByName(lowerForTest(t, src), "churn")
	if n := mapAliasedOverwriteDecCount(fn); n != 0 {
		t.Errorf("`m = mk(n)` emits %d same-pointer releases; a fresh producer's result aliases nothing, so that dec would free a handle the slot is the sole owner of", n)
	}
}
