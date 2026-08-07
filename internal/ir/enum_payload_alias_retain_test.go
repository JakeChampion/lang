package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// An enum payload that ALIASES a container the constructing function still
// owns must be retained, whichever of the three construction paths lowers
// it. Two of them used to skip the retain, so the container's exit drop
// freed the payload the caller had just been handed — a use-after-free that
// `FERN_RC_FREE_DEBUG=1` reports and the freelist turns into a segfault
// once the cell is recycled.
//
// These assert on the emitted op stream rather than on runtime behaviour
// because the runtime symptom depends on allocation reuse: the same missing
// inc is a crash, silent corruption, or nothing at all depending on what
// lands in the freed cell. The retain is the invariant; the crash is one
// symptom of its absence.

const aliasRetainTable = `struct Unit { name: string, factor: i32 }

function table(): Unit[] {
    return [Unit { name: "m", factor: 1 }, Unit { name: "km", factor: 1000 }];
}
`

// retainsPayloadAfterLoad reports whether the ops between the LAST element
// load and the enum construction that consumes it contain a retain. Anchoring
// on that window rather than counting the whole function keeps the assertion
// about this payload and indifferent to unrelated rc traffic elsewhere.
func retainsPayloadAfterLoad(fn *ir.Func, loadName string, construct func(ir.Op) bool) bool {
	lastLoad := -1
	for i, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == loadName {
			lastLoad = i
		}
	}
	if lastLoad < 0 {
		return false
	}
	for i := lastLoad + 1; i < len(fn.Ops); i++ {
		if construct(fn.Ops[i]) {
			return false
		}
		if fn.Ops[i].Kind == ir.OpRcInc {
			return true
		}
	}
	return false
}

func isAllocReuse(op ir.Op) bool {
	return op.Kind == ir.OpCallDirect && op.Str == "__alloc_reuse"
}

func isMakeSome(op ir.Op) bool { return op.Kind == ir.OpMakeSomeI32 }

// Pair-form: an Option/Result-returning function whose returns are all
// variant literals uses the register (tag, payload) ABI, which never reaches
// the Return lowering's transfer inc.
func TestPairFormReturnRetainsAliasedPayload(t *testing.T) {
	ip := lowerForTest(t, aliasRetainTable+`
function find(name: string): Option[Unit] {
    var us: Unit[] = table();
    for i in 0..us.len() {
        if (us[i].name == name) { return Some(us[i]); }
    }
    return None;
}

function main(): i32 {
    match (find("km")) {
        Some(u) => { return u.factor; },
        None => { return 0; },
    }
}
`)
	if !ip.PairForm["find"] {
		t.Fatal("find is not pair-form; this test no longer covers the pair-form path")
	}
	if !retainsPayloadAfterLoad(funcByName(ip, "find"), "__arr_idx_8", isMakeSome) {
		t.Error("find: Some(us[i]) hands the caller an element the exit sweep drops, with no retain between the load and the construction")
	}
}

// In-place enum reuse: `found = Some(us[i])` on a loop-carried local takes
// the __alloc_reuse overwrite path, which has its own payload stores.
func TestEnumReuseOverwriteRetainsAliasedPayload(t *testing.T) {
	ip := lowerForTest(t, aliasRetainTable+`
function last(name: string): Option[Unit] {
    var us: Unit[] = table();
    var found: Option[Unit] = None;
    for i in 0..us.len() {
        if (us[i].name == name) { found = Some(us[i]); }
    }
    return found;
}

function main(): i32 {
    match (last("km")) {
        Some(u) => { return u.factor; },
        None => { return 0; },
    }
}
`)
	if ip.PairForm["last"] {
		t.Fatal("last is pair-form; this test no longer covers the heap-box reuse path")
	}
	if !retainsPayloadAfterLoad(funcByName(ip, "last"), "__arr_idx_8", isAllocReuse) {
		t.Error("last: found = Some(us[i]) stores an element into the reused box with no retain, so the array's drop frees it")
	}
}

// The retain is for ALIASES only. A payload that is a fresh value — nobody's
// local, so no sweep can touch it — must not be inc'd, or the box out-counts
// its single owner and the cell leaks.
func TestPairFormReturnDoesNotRetainFreshPayload(t *testing.T) {
	ip := lowerForTest(t, aliasRetainTable+`
function fresh(n: i32): Option[Unit] {
    if (n == 0) { return None; }
    return Some(Unit { name: "fresh", factor: n });
}

function main(): i32 {
    match (fresh(7)) {
        Some(u) => { return u.factor; },
        None => { return 0; },
    }
}
`)
	if !ip.PairForm["fresh"] {
		t.Fatal("fresh is not pair-form; this test no longer covers the pair-form path")
	}
	if n := incCount(funcByName(ip, "fresh")); n != 0 {
		t.Errorf("fresh: got %d rc incs, want 0 (a freshly constructed payload has no other owner)", n)
	}
}
