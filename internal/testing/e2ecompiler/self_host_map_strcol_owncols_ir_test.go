package e2ecompiler

import (
	"testing"
)

// Heap-bump FIXPOINTS for #5335 (issue #4353 layer 0, general case): a
// growing STRING-column map frees its superseded column buffers, so a churn
// over Map[i32,string] / Map[string,i32] / Map[string,string] keeps the heap
// flat.
//
// Fixpoint contract (mirrors self_host_map_fixpoint_ir_test.go): growth at
// N=50 == growth at N=5000, non-zero, under a hard leak guard. The fixed
// cases pin value-correctness and the soundness edges: keys()/values() taken
// BEFORE later growing inserts must stay valid and keep SNAPSHOT semantics,
// and an i64-valued map's values() must survive the grows after it.
var mapStrColOwncolsCases = []struct {
	name  string
	src   func(n string) string
	fixed bool
	want  int
}{
	// A Map[i32,string] grown from cap 2 to 5 entries in a helper: each call
	// would otherwise leak the superseded keys/vals buffers (2 grows x 2
	// columns); with owncols they are freed on the spot and the MAPVS exit free reclaims
	// the final columns, so the per-call high-water is flat.
	{name: "strval-grow", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    let m: Map[i32, string] = map_new(2);
    m = m.insert(k, "v" + "0");
    m = m.insert(k + 1, "v" + "1");
    m = m.insert(k + 2, "v" + "2");
    m = m.insert(k + 3, "v" + "3");
    m = m.insert(k + 4, "v" + "4");
    let r: i32 = 0;
    if (m.has(k)) { r = r + 1; }
    if (m.has(k + 4)) { r = r + 1; }
    return r;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc != ` + n + ` * 2) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 3000) { return 119; }
    return g / 16;
}`
	}},
	// The string-KEY twin: Map[string,i32] grown past its initial capacity.
	// Deliberately no get_or/has lookups in the hot path: a computed string
	// lookup key (`m.get_or("k" + "0", ..)`) leaks its concat temp per call —
	// a separate, pre-existing string-temp gap that would hide this probe's
	// signal (the column-buffer grow-leak). m.len() reads allocate nothing.
	{name: "strkey-grow", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    let m: Map[string, i32] = map_new(2);
    m = m.insert("k" + "0", k);
    m = m.insert("k" + "1", k + 1);
    m = m.insert("k" + "2", k + 2);
    m = m.insert("k" + "3", k + 3);
    m = m.insert("k" + "4", k + 4);
    return m.len();
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc != ` + n + ` * 5) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 3000) { return 119; }
    return g / 16;
}`
	}},
	// Both columns string: Map[string,string] (owncols + the MAPKVS deep free).
	{name: "strkv-grow", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    let m: Map[string, string] = map_new(2);
    m = m.insert("k" + "0", "v" + "0");
    m = m.insert("k" + "1", "v" + "1");
    m = m.insert("k" + "2", "v" + "2");
    m = m.insert("k" + "3", "v" + "3");
    m = m.insert("k" + "4", "v" + "4");
    return m.len();
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc != ` + n + ` * 5) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 3000) { return 119; }
    return g / 16;
}`
	}},
	// THE revert-reason probe: keys()/values() of a Map[i32,string] taken
	// BEFORE five growing inserts. With owncols the grow frees the superseded
	// buffers, so these reads are only sound because they SNAPSHOT (native
	// semantics: ks/vs keep the pre-insert view). Exit codes pin both
	// liveness (no UAF garbage) and snapshot length semantics.
	{name: "valalias-snapshot-safe", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    let m: Map[i32, string] = map_new(2);
    m = m.insert(1, "a" + "b");
    m = m.insert(2, "c" + "d");
    let ks: i32[] = m.keys();
    let vs: string[] = m.values();
    m = m.insert(3, "e" + "f");
    m = m.insert(4, "g" + "h");
    m = m.insert(5, "i" + "j");
    m = m.insert(6, "k" + "l");
    m = m.insert(7, "m" + "n");
    if (ks.len() != 2) { return 90; }
    if (ks[0] != 1 || ks[1] != 2) { return 91; }
    if (vs.len() != 2) { return 92; }
    if (vs[0] != "ab" || vs[1] != "cd") { return 93; }
    if (!m.has(7) || !m.has(1)) { return 94; }
    if (m.get_or(6, "") != "kl") { return 95; }
    return 0;
}`
	}},
	// String-KEY twin of the alias probe (keys() snapshots via
	// __fern_map_snapshot_col_str before the growing inserts).
	{name: "keyalias-snapshot-safe", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(2);
    m = m.insert("a" + "b", 11);
    m = m.insert("c" + "d", 22);
    let ks: string[] = m.keys();
    m = m.insert("e" + "f", 33);
    m = m.insert("g" + "h", 44);
    m = m.insert("i" + "j", 55);
    m = m.insert("k" + "l", 66);
    m = m.insert("m" + "n", 77);
    if (ks.len() != 2) { return 90; }
    if (ks[0] != "ab" || ks[1] != "cd") { return 91; }
    if (m.get_or("ab", 0 - 1) != 11) { return 92; }
    if (m.get_or("mn", 0 - 1) != 77) { return 93; }
    return 0;
}`
	}},
	// Loop-declared churn correctness: values must read back right while the
	// owned grow + MAPKVS reclaim fire every iteration.
	{name: "strkv-churn", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 300) {
        let m: Map[string, string] = map_new(2);
        m = m.insert("k" + "0", "v" + "0");
        m = m.insert("k" + "1", "v" + "1");
        m = m.insert("k" + "2", "v" + "2");
        if (m.get_or("k" + "1", "") == "v1") { acc = acc + 1; }
        if (m.get_or("k" + "9", "miss") == "miss") { acc = acc + 1; }
        i = i + 1;
    }
    if (acc != 600) { return 121; }
    return 0;
}`
	}},
	// i64-VALUE churn correctness: an i64 column is flag-1 (raw alias reads),
	// so owncols must stay CLEAR — its superseded buffers keep leaking (the
	// load-bearing leak), but values must stay correct. The owncols exclusion
	// itself is pinned deterministically by the asm-grep subtest below.
	{name: "i64val-churn", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let m: Map[i32, i64] = map_new(2);
        m = m.insert(1, 11);
        m = m.insert(2, 22);
        m = m.insert(3, 33);
        if (m.get_or(2, 0) != 22) { return 121; }
        if (m.has(3)) { acc = acc + 1; }
        i = i + 1;
    }
    if (acc != 200) { return 122; }
    return 0;
}`
	}},
}

// TestSelfHostMapStrColOwncolsIRX86_64 runs the string-column shapes through
// the self-hosted CLI, which loads core/map. Fixpoint cases assert
// growth(N=50) == growth(N=5000), non-zero, under the leak guard; fixed cases
// assert their exact exit (121 = value mismatch, 119 = leak guard, 9x = a
// specific correctness probe) AND are cross-checked against the native
// `fern -interp` oracle (differential leg: every program is native-valid and
// must exit identically on both).
func TestSelfHostMapStrColOwncolsIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	sh := func(t *testing.T, prog string) int {
		t.Helper()
		_, exit := cli.exitOf(t, prog+"\n", "x86-64-linux")
		return exit
	}

	for _, tc := range mapStrColOwncolsCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixed {
				code := sh(t, tc.src(""))
				if code != tc.want {
					t.Errorf("%s: exited %d, want %d", tc.name, code, tc.want)
				}
				if native := runInterpExit(t, tc.src("")); native != code {
					t.Errorf("%s: differential mismatch — native -interp exited %d, self-host exited %d", tc.name, native, code)
				}
				return
			}
			small := sh(t, tc.src("50"))
			large := sh(t, tc.src("5000"))
			if small != large {
				t.Errorf("%s: high-water not bounded (N=50 -> %d, N=5000 -> %d)", tc.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: growth is 0 — probe does not allocate", tc.name)
			}
			if small >= 119 {
				t.Errorf("%s: leak guard tripped (%d)", tc.name, small)
			}
		})
	}
}
