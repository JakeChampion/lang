// An `own` param handed straight on to another `own` param — a re-move — used
// to pay a compensating retain whenever the transfer was not the param's
// textually-last occurrence, because move-on-call is whole-function and could
// claim only that one occurrence. The retain is correct and expensive: the
// callee then sees the value at rc>1, so its first append copies the whole
// buffer. One copy per call, which on the arm64 assembler's `.data` emitters
// came to 63 MB on a single compile (#6125).
//
// The sweep is emitted per RETURN SITE, so the transfer can be claimed there
// instead: this return skips the param, every other return still sweeps it.
// These cases pin that the claim fires where it should, does NOT fire where
// the transfer is not unique, and releases exactly once either way — a
// re-move that dropped the retain without excluding the sweep would
// over-release (caught by __rc_underflow_count), and one that excluded the
// sweep on a path that never transferred would leak (caught by the bump
// ceiling).
package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// ownRemoveCase is one `outer` body. The driver calls it 50 times as
// `p = outer(p, …)`, each call appending 20 elements to p's array field
// through `inner`, an `own`-param accumulator.
type ownRemoveCase struct {
	name string
	// outer is a function `outer(own p: P, k: i32): P`.
	outer string
	// wantCliff is the number of appends that copied a buffer with spare
	// capacity. One per call means every call re-copied the whole array.
	wantCliff int
}

var ownRemoveCases = []ownRemoveCase{
	// The target. `p` occurs twice: the transfer, and the bare `return p`
	// that is textually last. Before the per-site claim the transfer paid a
	// retain and every call copied — 49 crossings over 50 calls.
	{"A_transfer_then_bare_return", `function outer(own p: P, k: i32): P {
    if (k >= 0) { return inner(p, 20); }
    return p;
}`, 0},

	// Control: the transfer IS the last occurrence, so move-on-call already
	// claimed it whole-function. Must stay at zero — this is the shape the
	// per-site claim has to leave exactly as it found it.
	{"B_transfer_is_last_use", `function outer(own p: P, k: i32): P {
    if (k < 0) { return p; }
    return inner(p, 20);
}`, 0},

	// Control: several transfers on mutually exclusive paths plus a bare
	// return, which is the real arm64_gas_data_directive shape. Each return
	// transfers p exactly once, so each may claim it independently.
	{"C_many_transfer_returns", `function outer(own p: P, k: i32): P {
    if (k == 1) { return inner(p, 20); }
    if (k == 2) { return inner(p, 20); }
    if (k >= 0) { return inner(p, 20); }
    return p;
}`, 0},

	// Control: the transfer feeds a SECOND consuming call in the same return.
	// p is still transferred exactly once — the outer call consumes the inner
	// one's fresh result, not p — so the site qualifies and neither call
	// copies.
	{"D_nested_transfer", `function outer(own p: P, k: i32): P {
    return inner(inner(p, 10), 10);
}`, 0},
}

// src builds the driver. The length check runs before the counter is read, so
// a case that miscompiles reports 254 rather than a plausible cliff count.
// `probe` is the expression whose value becomes the exit code.
func (c ownRemoveCase) src(probe string) string {
	return fmt.Sprintf(`struct P { data: i32[], n: i32 }

function inner(own p: P, k: i32): P {
    var i: i32 = 0;
    while (i < k) { p = P { ...p, data: p.data.append(i) }; i = i + 1; }
    return p;
}

%s

function main(): i32 {
    var p: P = P { data: [], n: 0 };
    var j: i32 = 0;
    while (j < 50) { p = outer(p, j); j = j + 1; }
    if (p.data.len() != 1000) { return 254; }
    if (__rc_underflow_count() != 0) { return 253; }
    return %s;
}`, c.outer, probe)
}

func (c ownRemoveCase) check(t *testing.T, backend string, got int) {
	t.Helper()
	switch got {
	case 254:
		t.Fatalf("%s %s: the accumulator built the WRONG array — the cliff reading is meaningless until that is fixed", backend, c.name)
	case 253:
		t.Fatalf("%s %s: rc over-release — a transfer dropped its retain without excluding the sweep, so the value was released twice", backend, c.name)
	}
	if got != c.wantCliff {
		t.Errorf("%s %s: __arr_push_shared_count() = %d, want %d", backend, c.name, got, c.wantCliff)
	}
}

func TestX86_64OwnParamRemove(t *testing.T) {
	for _, c := range ownRemoveCases {
		t.Run(c.name, func(t *testing.T) {
			_, got := compileAndRunX86_64FreeOn(t, c.src("__arr_push_shared_count()"))
			c.check(t, "x86-64-linux", got)
		})
	}
}

func TestArm64OwnParamRemove(t *testing.T) {
	for _, c := range ownRemoveCases {
		t.Run(c.name, func(t *testing.T) {
			_, got := compileAndRunArm64(t, c.src("__arr_push_shared_count()"))
			c.check(t, "arm64-linux", got)
		})
	}
}

func TestWASMOwnParamRemove(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range ownRemoveCases {
		t.Run(c.name, func(t *testing.T) {
			c.check(t, "wasm32-wasi", runWasm(t, c.src("__arr_push_shared_count()")))
		})
	}
}

// A function whose returns may lower through the PAIR-FORM ABI (an
// `Option[i32]` return, one arm a variant literal and one a tail call) is
// excluded from the claim: that path emits its own sweep and never consults
// the per-site map, so dropping the argument's retain there would release a
// reference nothing else released. Whether pair-form actually fires depends on
// the target and the payload shape, which is exactly why this is asserted by
// RUNNING the program on every backend rather than by inspecting the IR on
// one: over-release shows up as a non-zero underflow count wherever it
// happens.
func TestOwnParamRemovePairFormReturnSafe(t *testing.T) {
	src := `function take(own xs: i32[]): Option[i32] { return Some(xs.len()); }
function outer(own xs: i32[], k: i32): Option[i32] {
    if (k >= 0) { return take(xs); }
    return None;
}
function main(): i32 {
    var total: i32 = 0;
    var j: i32 = 0;
    while (j < 20) {
        match (outer([1, 2, 3], j)) { Some(n) => { total = total + n; }, None => { total = total + 100; } }
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 253; }
    return total;
}`
	check := func(t *testing.T, backend string, got int) {
		t.Helper()
		if got == 253 {
			t.Errorf("%s: rc over-release on a pair-form-shaped return — the claim must not "+
				"fire where the sweep is emitted by another path", backend)
			return
		}
		if got != 60 {
			t.Errorf("%s: total = %d, want 60 (20 iterations x len 3)", backend, got)
		}
	}
	// One sub-test per backend, so a missing toolchain skips its own leg
	// instead of ending the function: the arm64 and wasm legs (and the
	// RcFreeEnabled toggle the wasm one needs) were unreachable in every lane.
	t.Run("x86_64", func(t *testing.T) {
		_, code := compileAndRunX86_64FreeOn(t, src)
		check(t, "x86-64-linux", code)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		_, code := compileAndRunArm64(t, src)
		check(t, "arm64-linux", code)
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		prev := ast.RcFreeEnabled
		ast.RcFreeEnabled = true
		defer func() { ast.RcFreeEnabled = prev }()
		check(t, "wasm32-wasi", runWasm(t, src))
	})
}

// The other direction: excluding a param from the sweep at a return that did
// NOT transfer it would leak one struct + one buffer per call, unbounded. The
// bump high-water mark is the instrument that sees it (allocations that are
// never freed cannot come from the freelist), and it is host-independent —
// unlike RSS, which varies 12x with the huge-page setting.
//
// `outer` here returns p untransferred on every iteration but the last, so a
// per-site claim that leaked onto the non-transferring path would show up as
// 50 iterations' worth of growth. Compared against the same driver whose outer
// never transfers at all, which is the leak-free baseline by construction.
func TestX86_64OwnParamRemoveNoLeakOnUntransferredPath(t *testing.T) {
	body := func(outer string) string {
		return fmt.Sprintf(`struct P { data: i32[], n: i32 }

function inner(own p: P, k: i32): P {
    var i: i32 = 0;
    while (i < k) { p = P { ...p, data: p.data.append(i) }; i = i + 1; }
    return p;
}

%s

function main(): i32 {
    var p: P = P { data: [], n: 0 };
    var j: i32 = 0;
    while (j < 50) { p = outer(p, j); j = j + 1; }
    if (__rc_underflow_count() != 0) { return 253; }
    var b: i64 = __heap_bump_bytes();
    return (b / 1000) as i32;
}`, outer)
	}
	// Takes the transfer only on the final iteration; the other 49 return p
	// untransferred and must sweep it.
	mixed := `function outer(own p: P, k: i32): P {
    if (k == 49) { return inner(p, 20); }
    return p;
}`
	// Never transfers — nothing to claim, so this is the baseline.
	never := `function outer(own p: P, k: i32): P {
    if (k > 100) { return inner(p, 20); }
    return p;
}`
	_, gotMixed := compileAndRunX86_64FreeOn(t, body(mixed))
	_, gotNever := compileAndRunX86_64FreeOn(t, body(never))
	if gotMixed == 253 || gotNever == 253 {
		t.Fatalf("rc over-release (mixed=%d never=%d)", gotMixed, gotNever)
	}
	// One transferring iteration allocates the 20-element array the other
	// case never builds, so a small excess is expected; a per-iteration leak
	// is not, and 50 of those would dwarf this margin.
	if gotMixed > gotNever+8 {
		t.Errorf("bump high-water = %d KB with one transferring path vs %d KB with none — "+
			"a return that did not transfer is skipping its sweep, leaking per call",
			gotMixed, gotNever)
	}
}
