package e2e

import (
	"testing"
)

func dynPrimBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for i32 {
    function show(self: Self): i32 { return self + 1; }
}
function go(k: i32): i32 { let d: dyn Show = k; return d.show(); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(41)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func dynPrimStringBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for string {
    function show(self: Self): i32 { return self.len(); }
}
function go(k: i32): i32 { let d: dyn Show = "hello"; return d.show() + k; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(3)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const dynLocalReturnMoveSrc = `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
struct Dot { r: i32 }
impl Show for i32 {
    function show(self: Self): i32 { return self + 1; }
}
impl Show for Dot {
    function show(self: Self): i32 { return self.r + 1; }
}
function mkprim(): dyn Show { let d: dyn Show = 41; return d; }
function mkstruct(): dyn Show { let d: dyn Show = Dot { r: 41 }; return d; }
function main(): i32 {
    let i: i32 = 0;
    while (i < 500) {
        let e = mkprim();
        if (e.show() != 42) { return 1; }
        let f = mkstruct();
        if (f.show() != 42) { return 2; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

func TestWASMDynTraitPrimHeapBumpBounded(t *testing.T) {
	small := runWasm(t, dynPrimBumpGrowthSrc("50"))
	large := runWasm(t, dynPrimBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn prim value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestWASMDynTraitPrimStringHeapBumpBounded(t *testing.T) {
	small := runWasm(t, dynPrimStringBumpGrowthSrc("50"))
	large := runWasm(t, dynPrimStringBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn string-payload value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWASMDynTraitLocalReturnMove(t *testing.T) {
	if got := runWasm(t, dynLocalReturnMoveSrc); got != 0 {
		t.Errorf("returned dyn local dispatch = %d, want 0 (1/2 = wrong dispatch value — freed cell; 99 = rc underflow)", got)
	}
}

func TestX86_64DynTraitPrimHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynPrimBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynPrimBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn prim value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestX86_64DynTraitPrimStringHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynPrimStringBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynPrimStringBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn string-payload value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestX86_64DynTraitLocalReturnMove(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, dynLocalReturnMoveSrc); code != 0 {
		t.Errorf("returned dyn local dispatch exited %d, want 0 (pre-fix: segfault — the exit sweep freed the returned cell)", code)
	}
}

func dynEnumPayloadBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
enum Op { Add(i32), Neg }
impl Show for Op {
    function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } }
}
function go(k: i32): i32 { let d: dyn Show = Add(41); return d.show() + k; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(3)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMDynTraitEnumPayloadHeapBumpBounded(t *testing.T) {
	small := runWasm(t, dynEnumPayloadBumpGrowthSrc("50"))
	large := runWasm(t, dynEnumPayloadBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn enum-payload growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestX86_64DynTraitEnumPayloadHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynEnumPayloadBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynEnumPayloadBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn enum-payload growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func dynStructFnExitBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
struct Dot { r: i32 }
impl Show for Dot {
    function show(self: Self): i32 { return self.r * 2; }
}
function go(k: i32): i32 { let d: dyn Show = Dot { r: 41 }; return d.show() + k; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(3)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMDynTraitStructFnExitHeapBumpBounded(t *testing.T) {
	small := runWasm(t, dynStructFnExitBumpGrowthSrc("50"))
	large := runWasm(t, dynStructFnExitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("fn-exit dyn struct growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestX86_64DynTraitStructFnExitHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynStructFnExitBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynStructFnExitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("fn-exit dyn struct growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitEnumPayloadHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynEnumPayloadBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, dynEnumPayloadBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn enum-payload growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func dynArrFnExitBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
struct Dot { r: i32 }
impl Show for i32 {
    function show(self: Self): i32 { return self + 1; }
}
impl Show for Dot {
    function show(self: Self): i32 { return self.r * 2; }
}
function go(k: i32): i32 { let xs: dyn Show[] = [k, Dot { r: k }, 7]; return xs[0].show() + xs[1].show() + xs[2].show(); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(3)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMDynTraitArrFnExitCorrect(t *testing.T) {
	src := `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
struct Dot { r: i32 }
impl Show for i32 {
    function show(self: Self): i32 { return self + 1; }
}
impl Show for Dot {
    function show(self: Self): i32 { return self.r * 2; }
}
function go(k: i32): i32 { let xs: dyn Show[] = [k, Dot { r: k }, 7]; return xs[0].show() + xs[1].show() + xs[2].show(); }
function main(): i32 {
    let i: i32 = 0;
    let bad: i32 = 0;
    while (i < 500) {
        if (go(3) != 18) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return bad;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("wasm function-exit dyn array churn = %d, want 0 (1 = wrong dispatch value; 99 = over-release)", got)
	}
}

func TestX86_64DynTraitArrFnExitHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynArrFnExitBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynArrFnExitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("function-exit dyn array growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitArrFnExitHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynArrFnExitBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, dynArrFnExitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("function-exit dyn array growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitPrimHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynPrimBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, dynPrimBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn prim value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitLocalReturnMove(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, dynLocalReturnMoveSrc); code != 0 {
		t.Errorf("returned dyn local dispatch exited %d, want 0 (pre-fix: segfault — the exit sweep freed the returned cell)", code)
	}
}
