package e2e

import (
	"fmt"
	"testing"
)

type ownRemoveCase struct {
	name      string
	outer     string
	wantCliff int
}

var ownRemoveCases = []ownRemoveCase{
	{"A_transfer_then_bare_return", `function outer(own p: P, k: i32): P {
    if (k >= 0) { return inner(p, 20); }
    return p;
}`, 0},

	{"B_transfer_is_last_use", `function outer(own p: P, k: i32): P {
    if (k < 0) { return p; }
    return inner(p, 20);
}`, 0},

	{"C_many_transfer_returns", `function outer(own p: P, k: i32): P {
    if (k == 1) { return inner(p, 20); }
    if (k == 2) { return inner(p, 20); }
    if (k >= 0) { return inner(p, 20); }
    return p;
}`, 0},

	{"D_nested_transfer", `function outer(own p: P, k: i32): P {
    return inner(inner(p, 10), 10);
}`, 0},

	{"E_early_transfer_last_also_transfers", `function outer(own p: P, k: i32): P {
    if (k % 2 == 0) { return inner(p, 20); }
    return inner(p, 20);
}`, 0},

	{"F_chained_transfers", `function outer(own p: P, k: i32): P {
    p = inner(p, 10);
    return inner(p, 10);
}`, 0},
}

func (c ownRemoveCase) src(probe string) string {
	return fmt.Sprintf(`struct P { data: i32[], n: i32 }

function inner(own p: P, k: i32): P {
    let i: i32 = 0;
    while (i < k) { p = P { ...p, data: p.data.append(i) }; i = i + 1; }
    return p;
}

%s

function main(): i32 {
    let p: P = P { data: [], n: 0 };
    let j: i32 = 0;
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
	for _, c := range ownRemoveCases {
		t.Run(c.name, func(t *testing.T) {
			c.check(t, "wasm32-wasi", runWasm(t, c.src("__arr_push_shared_count()")))
		})
	}
}

func TestOwnParamRemovePairFormReturnSafe(t *testing.T) {
	src := `function take(own xs: i32[]): Option[i32] { return Some(xs.len()); }
function outer(own xs: i32[], k: i32): Option[i32] {
    if (k >= 0) { return take(xs); }
    return None;
}
function main(): i32 {
    let total: i32 = 0;
    let j: i32 = 0;
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
	t.Run("x86_64", func(t *testing.T) {
		_, code := compileAndRunX86_64FreeOn(t, src)
		check(t, "x86-64-linux", code)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		_, code := compileAndRunArm64(t, src)
		check(t, "arm64-linux", code)
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		check(t, "wasm32-wasi", runWasm(t, src))
	})
}

func TestX86_64OwnParamRemoveNoLeakOnUntransferredPath(t *testing.T) {
	body := func(outer string) string {
		return fmt.Sprintf(`struct P { data: i32[], n: i32 }

function inner(own p: P, k: i32): P {
    let i: i32 = 0;
    while (i < k) { p = P { ...p, data: p.data.append(i) }; i = i + 1; }
    return p;
}

%s

function main(): i32 {
    let p: P = P { data: [], n: 0 };
    let j: i32 = 0;
    while (j < 50) { p = outer(p, j); j = j + 1; }
    if (__rc_underflow_count() != 0) { return 253; }
    let b: i64 = __heap_bump_bytes();
    return (b / 1000) as i32;
}`, outer)
	}
	mixed := `function outer(own p: P, k: i32): P {
    if (k == 49) { return inner(p, 20); }
    return p;
}`
	never := `function outer(own p: P, k: i32): P {
    if (k > 100) { return inner(p, 20); }
    return p;
}`
	_, gotMixed := compileAndRunX86_64FreeOn(t, body(mixed))
	_, gotNever := compileAndRunX86_64FreeOn(t, body(never))
	if gotMixed == 253 || gotNever == 253 {
		t.Fatalf("rc over-release (mixed=%d never=%d)", gotMixed, gotNever)
	}
	if gotMixed > gotNever+8 {
		t.Errorf("bump high-water = %d KB with one transferring path vs %d KB with none — "+
			"a return that did not transfer is skipping its sweep, leaking per call",
			gotMixed, gotNever)
	}
}
