package e2e

import (
	"testing"
)

var mapSetCountedSrcs = map[string]string{
	"string value": `import "core/map";
function work(i: i32): i32 {
    let stem: string = "alpha";
    let k: string = stem + "-key-long";
    let v: string = stem + "-value-long";
    let m: Map[string, string] = map_new(4);
    m = m.insert(k, v);
    return m.len() + k.len() + v.len();
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 31) { return 99; }
    return __rc_underflow_count();
}`,
	"array value": `import "core/map";
function work(i: i32): i32 {
    let stem: string = "alpha";
    let k: string = stem + "-key-long";
    let v: i32[] = [i, i + 1, i + 2];
    let m: Map[string, i32[]] = map_new(4);
    m = m.insert(k, v);
    return m.len() + k.len() + v.len();
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 18) { return 99; }
    return __rc_underflow_count();
}`,
	"struct value": `import "core/map";
struct Pt { name: string, x: i32 }
function work(i: i32): i32 {
    let stem: string = "alpha";
    let k: string = stem + "-key-long";
    let v: Pt = Pt { name: stem + "-point-name", x: i };
    let m: Map[string, Pt] = map_new(4);
    m = m.insert(k, v);
    return m.len() + k.len() + v.name.len();
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 31) { return 99; }
    return __rc_underflow_count();
}`,
	"map literal": `import "core/map";
function work(i: i32): i32 {
    let stem: string = "alpha";
    let k: string = stem + "-key-long";
    let v: i32[] = [i, i + 1, i + 2];
    let m: Map[string, i32[]] = Map { k: v };
    return m.len() + k.len() + v.len();
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 18) { return 99; }
    return __rc_underflow_count();
}`,
}

func mapSetCountedBumpSrc(n string) string {
	return `import "core/map";
function work(i: i32): i32 {
    let stem: string = "alpha";
    let k: string = stem + "-key-long";
    let v: i32[] = [i, i + 1, i + 2];
    let m: Map[string, i32[]] = map_new(4);
    m = m.insert(k, v);
    return m.len() + k.len() + v.len();
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + work(i); i = i + 1; }
    if (acc != ` + n + ` * 18) { return 99; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

var mapSetReadBackSrcs = map[string]string{
	"string value": `import "core/map";
function work(i: i32): i32 {
    let stem: string = "alpha";
    let k: string = stem + "-key-long";
    let v: string = stem + "-value-long";
    let m: Map[string, string] = map_new(4);
    m = m.insert(k, v);
    let got: string = m.get_or(k, "");
    return got.len() + i - i;
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 16) { return 99; }
    return __rc_underflow_count();
}`,
	"array value": `import "core/map";
function work(i: i32): i32 {
    let stem: string = "alpha";
    let k: string = stem + "-key-long";
    let v: i32[] = [i, i + 1, i + 2];
    let m: Map[string, i32[]] = map_new(4);
    m = m.insert(k, v);
    let got: i32[] = m.get_or(k, []);
    return got[2] - i + v.len();
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 5) { return 99; }
    return __rc_underflow_count();
}`,
	"struct value": `import "core/map";
struct Pt { name: string, x: i32 }
function work(i: i32): i32 {
    let stem: string = "alpha";
    let k: string = stem + "-key-long";
    let v: Pt = Pt { name: stem + "-point-name", x: i };
    let m: Map[string, Pt] = map_new(4);
    m = m.insert(k, v);
    let got: Pt = m.get_or(k, v);
    return got.name.len() + got.x - i;
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 16) { return 99; }
    return __rc_underflow_count();
}`,
	"nested map value stays live": `import "core/map";
function build(i: i32): Map[i32, Map[i32, i32]] {
    let inner: Map[i32, i32] = map_new(2);
    inner = inner.insert(i, i + 1);
    let outer: Map[i32, Map[i32, i32]] = map_new(2);
    outer = outer.insert(i, inner);
    return outer;
}
function work(i: i32): i32 {
    let outer: Map[i32, Map[i32, i32]] = build(i);
    match (outer.get(i)) {
        Some(inner) => { return inner.get_or(i, -1) - i; },
        None => { return -1000; },
    }
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200) { return 99; }
    return __rc_underflow_count();
}`,
}

func TestX86_64MapSetCountedStoreBalanced(t *testing.T) {
	for name, src := range mapSetCountedSrcs {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, src)
			if code != 0 {
				t.Fatalf("exit=%d, want 0 (99 = wrong sum, other = rc underflow)", code)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("expected allocations (the key / value are heap blocks), got 0")
			}
			if allocs != frees || live != 0 {
				t.Errorf("map store sources leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
			}
		})
	}
	for name, src := range mapSetReadBackSrcs {
		t.Run("read back "+name, func(t *testing.T) {
			if got := mustRunX86_64FreeOn(t, src); got != 0 {
				t.Errorf("want exit 0, got %d (99 = wrong value, other = rc underflow)", got)
			}
		})
	}
}

func TestArm64MapSetCountedStoreBalanced(t *testing.T) {
	for name, src := range mapSetCountedSrcs {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, src)
			if code != 0 {
				t.Fatalf("exit=%d, want 0 (99 = wrong sum, other = rc underflow)", code)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("expected allocations (the key / value are heap blocks), got 0")
			}
			if allocs != frees || live != 0 {
				t.Errorf("map store sources leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
			}
		})
	}
	for name, src := range mapSetReadBackSrcs {
		t.Run("read back "+name, func(t *testing.T) {
			if got := mustRunArm64FreeOn(t, src); got != 0 {
				t.Errorf("want exit 0, got %d (99 = wrong value, other = rc underflow)", got)
			}
		})
	}
}

func TestWasmMapSetCountedStoreBalanced(t *testing.T) {
	small := runWasm(t, mapSetCountedBumpSrc("50"))
	large := runWasm(t, mapSetCountedBumpSrc("5000"))
	if small != large {
		t.Errorf("map store bump should be bounded (sources reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	for name, src := range mapSetCountedSrcs {
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, src); got != 0 {
				t.Errorf("want exit 0, got %d (99 = wrong sum, other = rc underflow)", got)
			}
		})
	}
	for name, src := range mapSetReadBackSrcs {
		t.Run("read back "+name, func(t *testing.T) {
			if got := runWasm(t, src); got != 0 {
				t.Errorf("want exit 0, got %d (99 = wrong value, other = rc underflow)", got)
			}
		})
	}
}
