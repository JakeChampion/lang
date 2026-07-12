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
// the plain __fern_map_free) all to $__fern_map_release — so this validates the
// routing COMPILES and stays SOUND (correctness + aliased-key exclusion); it does
// not assert column flatness (the wasm $__fern_map_release string-K/V release is a
// separate pre-existing mechanism, a follow-up).
func TestSelfHostMapKsReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-ks-reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
				t.Errorf("map-ks-reclaim wasm IR %q = %d, want %d (88 = live value freed; 99 = over-release)", tc.name, got, tc.expected)
			}
		})
	}
}
