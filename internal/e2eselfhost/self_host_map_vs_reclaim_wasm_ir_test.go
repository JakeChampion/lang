package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapVsReclaimWasmIR is the wasm port of
// TestSelfHostMapVsReclaimIRX86_64 (#4353 cut 2). On wasm the new
// __fern_map_free_vs is a NO-OP distinction: wasm_helper_symbol routes BOTH it and
// the plain __fern_map_free to the same $__fern_map_release, which deep-releases
// the value column via the box's vis@28 flag. With the #4353 wasm bug-2 fix (the
// per-insert `vconsume` flag: $__fern_map_set TAKES a FRESH value temp's single
// ref instead of retaining, so $__fern_map_release's dec reclaims it rather than
// stranding it at rc 1) the value column is now FLAT for fresh values, so this
// test asserts DIFFERENTIAL flatness (string-valued map grows no more than an
// i32-valued map of the same shape) in addition to correctness + aliased-value
// exclusion (an aliased value stays vconsume=0 → retained → the source local's
// sweep balances it, no over-release).
func TestSelfHostMapVsReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-vs-reclaim wasm IR e2e")
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
		// DIFFERENTIAL value-column flatness: a Map[i32, string] built-and-dropped in
		// a callee (swept per call) must grow no more than the same-shape Map[i32,
		// i32] baseline — the fresh string values are now reclaimed (bug-2 fix), so
		// the only residual growth is the shared per-map buffer churn, which cancels.
		// Built without a lookup (m.get_or(k, "") would allocate a fresh "" default
		// temp that leaks independently, confounding the measurement).
		{"mapvs-value-column-flat-wasm", `function build_iv(n: i32): i32 {
    var m: Map[i32, string] = Map { 1: "a" + "b", 2: "c" + "d" };
    return 1;
}
function build_ii(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    return 1;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_iv(i) + build_ii(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_iv(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_ii(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		{"mapvs-value-correct-wasm", `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var m: Map[i32, string] = Map { 7: "hel" + "lo", 8: "wor" + "ld" };
        if (m.get_or(7, "").len() != 5) { bad = 1; }
        if (m.get_or(8, "").len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"mapvs-aliased-value-excluded-wasm", `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s: string = "aa" + "bb";
        var m: Map[i32, string] = Map { 1: s };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or(1, "").len() != 4) { bad = 1; }
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
				t.Errorf("map-vs-reclaim wasm IR %q = %d, want %d (1 = value column leaks vs i32 baseline; 88 = live value freed; 97 = acc guard; 99 = over-release)", tc.name, got, tc.expected)
			}
		})
	}
}
