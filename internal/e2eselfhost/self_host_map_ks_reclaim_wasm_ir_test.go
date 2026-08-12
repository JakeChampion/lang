package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapKsReclaimWasmIR is the wasm port of
// TestSelfHostMapKsReclaimIRX86_64 (#4353 string-KEY / both-column). On wasm the
// _ks / _kvs variants are a NO-OP distinction — wasm_helper_symbol routes them (and
// the plain __fern_map_free) all to $__fern_map_release, which deep-releases the
// key column via the box's kis@20 flag. With the #4353 wasm bug-2 fix (the
// per-insert `kconsume` flag, the key-column twin of `vconsume`: $__fern_map_set
// TAKES a FRESH key temp's single ref instead of retaining, so the release-side
// dec reclaims it) the key column is now FLAT, so this asserts DIFFERENTIAL
// flatness (string-keyed / both-column maps grow no more than an i32-keyed
// baseline) in addition to correctness + aliased-key exclusion (an aliased key
// stays kconsume=0 → retained → the source local's sweep balances it). It also
// covers the OVERWRITE path — the word-count / histogram m.set(computed_key, n)
// pattern where a recurring fresh key is re-inserted: the overwrite discards the
// incoming key, which the map must free ($kconsume) rather than leak, while an
// aliased recurring key must be left untouched.
func TestSelfHostMapKsReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-ks-reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// DIFFERENTIAL key-column flatness: a Map[string, i32] built-and-dropped in a
		// callee must grow no more than the same-shape Map[i32, i32] baseline (fresh
		// keys reclaimed by the bug-2 fix). Built without a lookup (m.has("a"+"b")
		// would allocate a fresh lookup-key temp that leaks independently).
		{"mapks-key-column-flat-wasm", `function build_sk(n: i32): i32 {
    var m: Map[string, i32] = Map { "a" + "b": 1, "c" + "d": 2 };
    return 1;
}
function build_ik(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    return 1;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_sk(i) + build_ik(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_sk(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_ik(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		// DIFFERENTIAL both-column flatness: a Map[string, string] with fresh keys AND
		// values reclaims both columns (kconsume + vconsume), matching the i32 baseline.
		{"mapkvs-both-columns-flat-wasm", `function build_ss(n: i32): i32 {
    var m: Map[string, string] = Map { "a" + "b": "x" + "y", "c" + "d": "z" + "w" };
    return 1;
}
function build_ii(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    return 1;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_ss(i) + build_ii(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_ss(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_ii(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		{"mapks-key-correct-wasm", `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var m: Map[string, string] = Map { "hel" + "lo": "aa" + "bb", "wor" + "ld": "cc" + "dd" };
        if (m.get_or("hello", "").len() != 4) { bad = 1; }
        if (m.get_or("world", "").len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"mapks-aliased-key-excluded-wasm", `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s: string = "aa" + "bb";
        var m: Map[string, i32] = Map { s: 7 };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or("aabb", 0) != 7) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		// OVERWRITE with a recurring FRESH key (the classic word-count /
		// histogram m.set(computed_key, n) pattern): each re-insert of the same
		// key overwrites the slot and discards the incoming fresh key temp. The
		// overwrite path must free that discarded fresh key ($kconsume), else it
		// leaks one key box per overwrite. Differential against an i32-keyed map
		// doing the same overwrites (i32 keys carry no rc → no leak).
		{"mapks-overwrite-fresh-key-flat-wasm", `function build_sk_over(n: i32): i32 {
    var m: Map[string, i32] = Map { "wo" + "rd": 0 };
    var j: i32 = 0;
    while (j < 8) { m.set("wo" + "rd", j); j = j + 1; }
    return 1;
}
function build_ik_over(n: i32): i32 {
    var m: Map[i32, i32] = Map { 7: 0 };
    var j: i32 = 0;
    while (j < 8) { m.set(7, j); j = j + 1; }
    return 1;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_sk_over(i) + build_ik_over(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_sk_over(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_ik_over(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		// Overwrite correctness + no over-release: the recurring fresh key reads
		// back the LAST value and len stays 1 through the churn.
		{"mapks-overwrite-fresh-key-correct-wasm", `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var m: Map[string, i32] = Map { "wo" + "rd": 0 };
        var j: i32 = 0;
        while (j < 8) { m.set("wo" + "rd", j); j = j + 1; }
        if (m.get_or("word", 0) != 7) { bad = 1; }
        if (m.len() != 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		// Overwrite with an ALIASED key (a bare local reused across re-inserts):
		// kconsume=0, so the map must NOT free the key — the local stays valid
		// through the overwrites and there is no over-release.
		{"mapks-overwrite-aliased-key-wasm", `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var key: string = "wo" + "rd";
        var m: Map[string, i32] = Map { "wo" + "rd": 0 };
        var j: i32 = 0;
        while (j < 8) { m.set(key, j); j = j + 1; }
        if (key.len() != 4) { bad = 1; }
        if (m.get_or("word", 0) != 7) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("map-ks-reclaim wasm IR %q = %d, want %d (1 = key/both column leaks vs i32 baseline; 88 = live value freed; 97 = acc guard; 99 = over-release)", tc.name, got, tc.expected)
			}
		})
	}
}
