package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapKeysSnapshotWasmIR is the wasm leg of the #4353 slice-1+2
// coupled change. The wasm map has ALWAYS snapshotted keys()/values()
// ($__fern_map_keys / $__fern_map_values) and already frees superseded buffers
// on grow, so the snapshot-semantics case pins behaviour that must simply stay
// put under the new op_map_keys/op_map_values element flag (wasm ignores it)
// and the op_map_set owncols width bit (wasm masks it off its vconsume /
// kconsume bit reads). New behaviour covered here: the `for (k, v) in m` /
// `for k in m.keys()` scalar-column snapshots are now RELEASED right after
// the loop (they were leaked per loop on wasm before), so the iteration churn
// case asserts differential flatness against a no-iteration baseline.
func TestSelfHostMapKeysSnapshotWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-keys-snapshot wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
		// SNAPSHOT SEMANTICS (matches native + the register backends after
		// #4353): keys()/values() taken before later inserts/overwrites show
		// the pre-set state, including across a rehash-triggering growth.
		{"map-keys-snapshot-semantics-wasm", `function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20 };
    var ks = m.keys();
    var vs: i32[] = m.values();
    m.set(9, 90);
    m.set(10, 100);
    m.set(11, 110);
    m.set(1, 11);
    if (ks.len() != 2) { return 10; }
    if (vs.len() != 2) { return 11; }
    var sv: i32 = 0;
    var i: i32 = 0;
    while (i < vs.len()) { sv = sv + vs[i]; i = i + 1; }
    if (sv != 30) { return 12; }
    if (m.len() != 5) { return 13; }
    if (m.get_or(1, 0) != 11) { return 14; }
    if (m.get_or(10, 0) != 100) { return 15; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
		// MUTATION DURING `for (k, v) in m`: iterates the entry-time snapshot
		// (wasm's historical semantics, now shared by the register backends).
		{"map-kv-iter-mutate-snapshot-wasm", `function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
    var total: i32 = 0;
    for (k, v) in m {
        total = total + k + v;
        m.set(k + 100, v);
    }
    if (total != 66) { return 50; }
    if (m.len() != 6) { return 51; }
    if (m.get_or(102, 0) != 20) { return 52; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
		// `for (k, v) in m` churn, DIFFERENTIAL against the same build without
		// the iteration: the two per-loop column snapshots are released after
		// the loop (they used to leak per loop on wasm), so the iterating
		// build must not grow measurably faster than the plain one.
		{"map-kv-iter-churn-flat-wasm", `function build_it(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2, 3: 4, 5: 6 };
    var t: i32 = 0;
    for (k, v) in m { t = t + k + v; }
    return t;
}
function build_plain(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2, 3: 4, 5: 6 };
    return m.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_it(i) + build_plain(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_it(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_plain(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		// i32/i32 grow churn: wasm already freed superseded buffers on grow —
		// pins that the owncols width bit changes nothing here (differential
		// against a non-growing build of the same shape).
		{"map-i32-grow-churn-wasm", `function build_grow(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2 };
    var j: i32 = 0;
    while (j < 12) { m.set(j + 10, j * 2); j = j + 1; }
    if (m.has(15)) { return m.len(); }
    return 0;
}
function build_small(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2 };
    return m.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_grow(i) + build_small(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_grow(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_small(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		// #4353 SLICE 3 (string columns): iterating a string-KEYED map with
		// `for k in m.keys()` now takes a RETAINING snapshot ($__fern_map_keys_inc)
		// released deep after the loop ($__fern_arr_dec_ptr) — before slice 3
		// the wasm snapshot leaked its buffer every loop AND the string boxes
		// were double-counted. Churn must be FLAT vs a no-iteration baseline,
		// and __rc_underflow()==0 proves the deep release never over-frees a
		// map-owned key. The keys must still all be seen (correctness).
		{"map-string-keys-iter-churn-flat-wasm", `function build_iter(n: i32): i32 {
    var m: Map[string, i32] = Map { "a" + "x": 1, "b" + "y": 2, "c" + "z": 3 };
    var seen: i32 = 0;
    for k in m.keys() { seen = seen + k.len(); }
    return seen;
}
function build_noiter(n: i32): i32 {
    var m: Map[string, i32] = Map { "a" + "x": 1, "b" + "y": 2, "c" + "z": 3 };
    return m.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_iter(i) + build_noiter(i); i = i + 1; }
    var s0: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_iter(j); j = j + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_noiter(k); k = k + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s1 - s0) > (s2 - s1) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		// String-VALUED map iterated via `for (k, v) in m`: the value column
		// snapshots+retains and deep-releases too. Same flatness + underflow
		// contract; the values must be seen (each is length 2).
		{"map-string-values-iter-churn-flat-wasm", `function build_iter(n: i32): i32 {
    var m: Map[i32, string] = Map { 1: "a" + "a", 2: "b" + "b" };
    var seen: i32 = 0;
    for (k, v) in m { seen = seen + v.len(); }
    return seen;
}
function build_noiter(n: i32): i32 {
    var m: Map[i32, string] = Map { 1: "a" + "a", 2: "b" + "b" };
    return m.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_iter(i) + build_noiter(i); i = i + 1; }
    var s0: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_iter(j); j = j + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_noiter(k); k = k + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s1 - s0) > (s2 - s1) + 8192) { return 1; }
    if (acc < 0) { return 97; }
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
				t.Errorf("map-keys-snapshot wasm IR %q = %d, want %d (1 = leak vs baseline; 99 = over-release; other = correctness step)", tc.name, got, tc.expected)
			}
		})
	}
}
