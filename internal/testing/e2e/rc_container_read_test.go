package e2e

import (
	"testing"
)

const bumpVerdictFlat = 0

func mapGetVerdictSrc() string {
	return `import "core/map";
function lookups(index: Map[string, i32], n: i32): i32 {
    let i: i32 = 0;
    let hits: i32 = 0;
    while (i < n) {
        match (index.get("alpha")) {
            Some(g) => { hits = hits + g; },
            None => { hits = hits - 1; }
        }
        match (index.get("absent")) {
            Some(g) => { hits = hits + g; },
            None => { hits = hits + 2; }
        }
        i = i + 1;
    }
    return hits;
}
function main(): i32 {
    let index: Map[string, i32] = map_new(64);
    index = index.insert("alpha", 1);
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = lookups(index, 400);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = lookups(index, 800);
    let b2: i64 = __heap_bump_bytes();
    if (x != 1200) { return 991; }
    if (y != 2400) { return 992; }
    // The map still owns its value after 3600 borrowed reads.
    match (index.get("alpha")) { Some(g) => { if (g != 1) { return 993; } }, None => { return 994; } }
    if ((b2 - b1) > (b1 - b0)) { return 1; }
    return __rc_underflow_count();
}`
}

func seededAccumulatorVerdictSrc() string {
	return `function build(words: string[]): string {
    let line: string = words[0];
    let g: i32 = 1;
    while (g < words.len()) { line = line + "  " + words[g]; g = g + 1; }
    return line;
}
function rounds(words: string[], n: i32): i32 {
    let i: i32 = 0;
    let t: i32 = 0;
    while (i < n) { t = t + build(words).len(); i = i + 1; }
    return t;
}
function main(): i32 {
    let words: string[] = ["alpha", "beta", "gamma", "delta"];
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(words, 400);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(words, 800);
    let b2: i64 = __heap_bump_bytes();
    if (x != 10000) { return 991; }
    if (y != 20000) { return 992; }
    if ((b2 - b1) > (b1 - b0)) { return 1; }
    return __rc_underflow_count();
}`
}

func TestX86_64MapGetBounded(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, mapGetVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("map-get bump should be bounded (#6561): verdict=%d", code)
	}
}

func TestArm64MapGetBounded(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, mapGetVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("map-get bump should be bounded (#6561): verdict=%d", code)
	}
}

func TestWASMMapGetBounded(t *testing.T) {
	if code := runWasm(t, mapGetVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("map-get bump should be bounded (#6561): verdict=%d", code)
	}
}

func TestX86_64SeededAccumulatorBounded(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, seededAccumulatorVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("seeded-accumulator bump should be bounded (#6567): verdict=%d", code)
	}
}

func TestWASMSeededAccumulatorBounded(t *testing.T) {
	if code := runWasm(t, seededAccumulatorVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("seeded-accumulator bump should be bounded (#6567): verdict=%d", code)
	}
}
