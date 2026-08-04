package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Map loop-var reclamation (RC-Perceus). A `var m = map_new(8)`
// re-declared in a loop reuses one slot per iteration. The exit sweep
// already reclaims an owned Map (value column + string-key column + buf
// + handle via __map_drop_values / __drop_map_str_* / __fern_map_drop),
// but emitVarReinitDropOld routed Map through emitStructEnumSlotDrop,
// whose dropFnNameFor declines Map → a flat __fern_rc_dec that frees
// nothing. So every iteration but the last leaked the entire map
// structure (a __heap_bump_bytes() probe measured 6400 B → 640000 B).
// The fix routes Map loop-var reinit through the shared emitMapSlotDrop
// (extracted from the exit sweep), so the bump high-water stays FLAT.

func mapReinitBumpSrc(n string) string {
	return `import "core/map";
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var m: Map[i32, i32] = map_new(8);
        m = m.insert(i, i * 2);
        m = m.insert(i + 1, i * 3);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// String key + value columns must also reclaim (and not over-release).
// Returns 0 iff value-correct AND no over-release over 200 iterations.
const mapReinitUnderflowSrc = `import "core/map";
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var m: Map[string, i32] = map_new(8);
        m = m.insert("alpha", i);
        m = m.insert("beta", i + 1);
        acc = acc + m.get_or("alpha", 0) + m.get_or("beta", 0);
        i = i + 1;
    }
    if (acc != 40000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64MapReinitReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, mapReinitBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, mapReinitBumpSrc("5000"))
	if small != large {
		t.Errorf("map loop-var bump should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapReinitUnderflowSrc); code != 0 {
		t.Errorf("map reinit string K/V: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64MapReinitReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, mapReinitBumpSrc("50"))
	large := mustRunArm64FreeOn(t, mapReinitBumpSrc("5000"))
	if small != large {
		t.Errorf("map loop-var bump should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, mapReinitUnderflowSrc); code != 0 {
		t.Errorf("map reinit string K/V: code=%d", code)
	}
}

func TestWASMMapReinitReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, mapReinitBumpSrc("50"))
	large := runWasm(t, mapReinitBumpSrc("5000"))
	if small != large {
		t.Errorf("map loop-var bump should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, mapReinitUnderflowSrc); got != 0 {
		t.Errorf("map reinit string K/V: got %d", got)
	}
}
