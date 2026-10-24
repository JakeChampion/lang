package e2e

import (
	"testing"
)

const mapStringColumnSrc = `import "core/map";
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 500) {
        let stem: string = "alpha";
        let m: Map[string, string] = map_new(4);
        m = m.insert(stem + "-key-long", stem + "-value-long");
        match (m.get(stem + "-key-long")) { Some(v) => { acc = acc + v.len(); }, None => {} }
        if (m.contains(stem + "-key-long")) { acc = acc + 1; }
        acc = acc + m.get_or(stem + "-key-long", stem + "-fallback!").len();
        i = i + 1;
    }
    if (acc < 0) { return 1; }
    return 0;
}`

func mapStringColumnBumpSrc(n string) string {
	return `import "core/map";
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let stem: string = "alpha";
        let m: Map[string, string] = map_new(4);
        m = m.insert(stem + "-key-long", stem + "-value-long");
        match (m.get(stem + "-key-long")) { Some(v) => { acc = acc + v.len(); }, None => {} }
        acc = acc + m.get_or(stem + "-key-long", stem + "-fallback!").len();
        i = i + 1;
    }
    if (acc < 0) { return acc; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const mapStringColumnUnderflowSrc = `import "core/map";
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let stem: string = "alpha";
        let m: Map[string, string] = map_new(4);
        m = m.insert(stem + "-key-long", stem + "-value-long");
        let got: string = m.get_or(stem + "-key-long", "");
        acc = acc + got.len();
        match (m.get(stem + "-key-long")) { Some(v) => { acc = acc + v.len(); }, None => { acc = acc + 100; } }
        i = i + 1;
    }
    // "alpha-value-long" is 16 chars, read twice per iteration.
    if (acc != 200 * 32) { return 99; }
    return __rc_underflow_count();
}`

const mapWideValueFallbackSrc = `import "core/map";
function main(): i32 {
    let i: i32 = 0;
    let acc: i64 = 0;
    while (i < 500) {
        let m: Map[i32, i64] = map_new(4);
        m = m.insert(7, 1234567890123);
        acc = acc + m.get_or(7, 0);
        i = i + 1;
    }
    if (acc != 500 * 1234567890123) { return 99; }
    return 0;
}`

const mapAliasedOverwriteSrc = `import "core/map";
function mk(): i32 {
    let stem: string = "a";
    let m: Map[string, string] = map_new(8);
    let k: string = stem + "-key-long-one";
    m = m.insert(k, stem + "-value-long-one");
    let snap: Map[string, string] = m;
    m = m.insert(k, stem + "-value-long-two");
    let ok: i32 = 0;
    if (snap.get_or(k, "") == "a-value-long-one") { ok = ok + 1; }
    if (m.get_or(k, "") == "a-value-long-two") { ok = ok + 2; }
    if (snap.len() == 1) { ok = ok + 4; }
    if (m.len() == 1) { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

const mapAliasedTwoEntrySrc = `import "core/map";
function mk(): i32 {
    let stem: string = "a";
    let m: Map[string, string] = map_new(8);
    let k1: string = stem + "-key-long-one";
    let k2: string = stem + "-key-long-two";
    m = m.insert(k1, stem + "-value-long-one");
    m = m.insert(k2, stem + "-value-long-untouched");
    let snap: Map[string, string] = m;
    m = m.insert(k1, stem + "-value-long-two");
    let ok: i32 = 0;
    if (snap.get_or(k1, "") == "a-value-long-one") { ok = ok + 1; }
    if (m.get_or(k1, "") == "a-value-long-two") { ok = ok + 2; }
    if (snap.get_or(k2, "") == "a-value-long-untouched") { ok = ok + 4; }
    if (m.get_or(k2, "") == "a-value-long-untouched") { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

func mapAliasedKeyReadSrc(verb string) string {
	read := ""
	switch verb {
	case "get_or":
		read = `    if (m.get_or(k, "") == "a-value-long-one") { ok = ok + 1; }`
	case "get":
		read = `    match (m.get(k)) { Some(v) => { if (v == "a-value-long-one") { ok = ok + 1; } }, None => {} }`
	case "contains":
		read = `    if (m.contains(k)) { ok = ok + 1; }`
	}
	return `import "core/map";
function mk(): i32 {
    let stem: string = "a";
    let m: Map[string, string] = map_new(8);
    let k: string = stem + "-key-long-one";
    m = m.insert(k, stem + "-value-long-one");
    let ok: i32 = 0;
` + read + `
    return ok;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200) { return 97; }
    return __rc_underflow_count();
}`
}

func assertMapStringCensus(t *testing.T, name string, run func(string) (string, string, int), src string) {
	t.Helper()
	_, stderr, code := run(src)
	if code != 0 {
		t.Fatalf("%s: exit=%d, want 0 (97=wrong value, >0=over-release)", name, code)
	}
	a, f, live := parseLeakCheckLine(t, stderr)
	if a == 0 {
		t.Fatalf("%s: no allocations — the probe is not exercising heap strings", name)
	}
	if a != f || live != 0 {
		t.Errorf("%s leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", name, a, f, live)
	}
}

func TestX86_64MapStringColumnReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, mapStringColumnSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (keys and values are heap strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("map string columns leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapStringColumnUnderflowSrc); code != 0 {
		t.Errorf("map string reclaim: code=%d (99=wrong value, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapWideValueFallbackSrc); code != 0 {
		t.Errorf("wide-value get_or: code=%d (99=wrong value)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapAliasedOverwriteSrc); code != 0 {
		t.Errorf("aliased overwrite: code=%d (97=wrong value, >0=over-release, signal=the freed value came back)", code)
	}
	small := mustRunX86_64FreeOn(t, mapStringColumnBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, mapStringColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapAliasedTwoEntrySrc); code != 0 {
		t.Errorf("aliased two-entry: code=%d (97=wrong value, >0=over-release, signal=a shared value column was freed twice)", code)
	}
	x86 := func(src string) (string, string, int) { return runLeakCheckX86_64(t, src) }
	assertMapStringCensus(t, "aliased overwrite", x86, mapAliasedOverwriteSrc)
	assertMapStringCensus(t, "aliased two-entry", x86, mapAliasedTwoEntrySrc)
	for _, verb := range []string{"get_or", "get", "contains"} {
		assertMapStringCensus(t, "aliased key read via "+verb, x86, mapAliasedKeyReadSrc(verb))
	}
}

func TestArm64MapStringColumnReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, mapStringColumnSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (keys and values are heap strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("map string columns leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapStringColumnUnderflowSrc); code != 0 {
		t.Errorf("map string reclaim: code=%d (99=wrong value, >0=over-release)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapWideValueFallbackSrc); code != 0 {
		t.Errorf("wide-value get_or: code=%d (99=wrong value)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapAliasedOverwriteSrc); code != 0 {
		t.Errorf("aliased overwrite: code=%d (97=wrong value, >0=over-release, signal=the freed value came back)", code)
	}
	small := mustRunArm64FreeOn(t, mapStringColumnBumpSrc("50"))
	large := mustRunArm64FreeOn(t, mapStringColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapAliasedTwoEntrySrc); code != 0 {
		t.Errorf("aliased two-entry: code=%d (97=wrong value, >0=over-release, signal=a shared value column was freed twice)", code)
	}
	arm := func(src string) (string, string, int) { return runLeakCheckArm64(t, src) }
	assertMapStringCensus(t, "aliased overwrite", arm, mapAliasedOverwriteSrc)
	assertMapStringCensus(t, "aliased two-entry", arm, mapAliasedTwoEntrySrc)
	for _, verb := range []string{"get_or", "get", "contains"} {
		assertMapStringCensus(t, "aliased key read via "+verb, arm, mapAliasedKeyReadSrc(verb))
	}
}

func TestWASMMapStringColumnReclaim(t *testing.T) {
	small := runWasm(t, mapStringColumnBumpSrc("50"))
	large := runWasm(t, mapStringColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, mapStringColumnUnderflowSrc); got != 0 {
		t.Errorf("map string reclaim: code=%d (99=wrong value, >0=over-release)", got)
	}
	if got := runWasm(t, mapWideValueFallbackSrc); got != 0 {
		t.Errorf("wide-value get_or: code=%d (99=wrong value)", got)
	}
	if got := runWasm(t, mapAliasedOverwriteSrc); got != 0 {
		t.Errorf("aliased overwrite: code=%d (97=wrong value, >0=over-release)", got)
	}
	if got := runWasm(t, mapAliasedTwoEntrySrc); got != 0 {
		t.Errorf("aliased two-entry: code=%d (97=wrong value, >0=over-release)", got)
	}
	wasm := func(src string) (string, string, int) { return runLeakCheckWasm(t, src, false) }
	assertMapStringCensus(t, "aliased overwrite", wasm, mapAliasedOverwriteSrc)
	assertMapStringCensus(t, "aliased two-entry", wasm, mapAliasedTwoEntrySrc)
	for _, verb := range []string{"get_or", "get", "contains"} {
		assertMapStringCensus(t, "aliased key read via "+verb, wasm, mapAliasedKeyReadSrc(verb))
	}
}

const genericEnumPayloadLeakSrc = `struct Item { name: string, v: i32 }
function mk(i: i32, sfx: string): Option[Item] {
    return Some(Item { name: "item-with-a-long-name" + sfx, v: i });
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 500) {
        let sfx: string = "s";
        match (mk(i, sfx)) {
            Some(it) => { acc = acc + it.v + it.name.len(); },
            None => {},
        }
        i = i + 1;
    }
    if (acc < 0) { return 1; }
    return 0;
}`

const nestedNonUniformFieldLeakSrc = `struct Inner { data: i32[], tag: string }
struct Outer { inner: Inner, id: i32 }
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 500) {
        let stem: string = "tag";
        let o: Outer = Outer { inner: Inner { data: [1, 2, 3], tag: stem + "-of-this-inner" }, id: i };
        acc = acc + o.inner.data.len() + o.inner.tag.len();
        i = i + 1;
    }
    if (acc < 0) { return 1; }
    return 0;
}`

func TestX86_64GenericEnumPayloadNoLeak(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, genericEnumPayloadLeakSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (box + Item + name), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("generic enum payload leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestX86_64NestedNonUniformFieldNoLeak(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, nestedNonUniformFieldLeakSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (Outer + Inner + data + tag), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("nested non-uniform fields leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestArm64GenericEnumPayloadNoLeak(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, genericEnumPayloadLeakSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("generic enum payload leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestArm64NestedNonUniformFieldNoLeak(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, nestedNonUniformFieldLeakSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("nested non-uniform fields leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}
