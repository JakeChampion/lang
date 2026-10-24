package e2e

import (
	"testing"
)

func mapIntermediateBoundBumpSrc(n string) string {
	return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let m: Map[i32, i32] = mk(i);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
}

func mapIntermediateDiscardBumpSrc(n string) string {
	return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
}

const mapIntermediateUnderflowSrc = `import "core/map";
function mk(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m;
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 300) {
        let m: Map[i32, i32] = mk(i);
        acc = acc + m.get_or(i, 0) + m.get_or(i + 1, 0);
        i = i + 1;
    }
    if (acc != 5 * (300 * 299 / 2)) { return 121; }
    return __rc_underflow_count();
}`

const mapIntermediateParamReceiverSrc = `import "core/map";
function grow(m: Map[i32, i32], k: i32): Map[i32, i32] {
    m = m.insert(k, k * 7);
    return m;
}
function main(): i32 {
    let base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 300) {
        let g: Map[i32, i32] = grow(base, i + 2);
        acc = acc + g.get_or(1, 0);
        i = i + 1;
    }
    if (base.get_or(1, 0) != 11) { return 121; }
    if (acc != 300 * 11) { return 120; }
    return __rc_underflow_count();
}`

const mapIntermediateParamReceiverLiteralSrc = `import "core/map";
function grow(m: Map[i32, i32], k: i32): Map[i32, i32] {
    m = m.insert(k, k * 7);
    return m;
}
function main(): i32 {
    let base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 300) {
        let g: Map[i32, i32] = grow(base, 2);
        acc = acc + g.get_or(1, 0);
        i = i + 1;
    }
    if (base.get_or(1, 0) != 11) { return 121; }
    if (acc != 300 * 11) { return 120; }
    return __rc_underflow_count();
}`

const mapIntermediatePtrValueParamSrc = `import "core/map";
function mk(xs: i32[], k: i32): Map[i32, i32[]] {
    let m: Map[i32, i32[]] = map_new(8);
    m = m.insert(k, xs);
    return m;
}
function main(): i32 {
    let keep: i32[] = [7, 8, 9];
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 300) {
        let t: Map[i32, i32[]] = mk(keep, i);
        let got: i32[] = t.get_or(i, []);
        acc = acc + got.len();
        i = i + 1;
    }
    if (keep[0] != 7 || keep[1] != 8 || keep[2] != 9 || keep.len() != 3) { return 121; }
    if (acc != 900) { return 120; }
    return __rc_underflow_count();
}`

func runMapIntermediateChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, tc := range []struct {
		name string
		src  func(string) string
	}{
		{"bound", mapIntermediateBoundBumpSrc},
		{"discarded", mapIntermediateDiscardBumpSrc},
	} {
		small := run(t, tc.src("50"))
		large := run(t, tc.src("5000"))
		if small != large {
			t.Errorf("%s map-intermediate bump should be bounded: N=50 -> %d, N=5000 -> %d", tc.name, small, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", tc.name)
		}
		if small >= 119 {
			t.Errorf("%s: growth guard tripped (%d) — probe scaling needs adjusting", tc.name, small)
		}
	}
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"credited-underflow", mapIntermediateUnderflowSrc},
		{"param-receiver-negative", mapIntermediateParamReceiverSrc},
		{"param-receiver-literal-negative", mapIntermediateParamReceiverLiteralSrc},
		{"ptr-value-param-negative", mapIntermediatePtrValueParamSrc},
	} {
		if code := run(t, tc.src); code != 0 {
			t.Errorf("%s: code=%d (121/120=value mismatch, >0=over-release)", tc.name, code)
		}
	}
}

func TestX86_64MapIntermediateReclaim(t *testing.T) {
	runMapIntermediateChecks(t, mustRunX86_64FreeOn)
}

func TestArm64MapIntermediateReclaim(t *testing.T) {
	runMapIntermediateChecks(t, mustRunArm64FreeOn)
}

func TestWASMMapIntermediateReclaim(t *testing.T) {
	runMapIntermediateChecks(t, func(t *testing.T, src string) int {
		return runWasm(t, src)
	})
}
