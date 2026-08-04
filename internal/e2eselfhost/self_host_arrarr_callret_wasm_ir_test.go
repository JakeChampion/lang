package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArrArrCallRetReclaimWasmIR is the wasm port of
// TestSelfHostArrArrCallRetReclaimIRX86_64 (#4355 slice 10): the "AAC:"
// registry admission of call-initialised arr-of-arr locals, plus the fn-scope
// single-sweep pin (the slice-9 double-sweep fix), on the wasm IR path
// ($__fern_arr_dec_ptr / $__fern_arr_dec_ptr2 routing unchanged from slice 9).
func TestSelfHostArrArrCallRetReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arrarr-callret wasm IR e2e")
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
		{"arrarr-callret-str-flat-wasm", `function mk(i: i32): string[][] {
    return [["a" + "b"], ["c" + "d", "e" + "f"]];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g: string[][] = mk(i);
        acc = acc + g.len() + g[0][0].len();
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1500) {
        var g2: string[][] = mk(j);
        acc = acc + g2.len() + g2[1][1].len();
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		{"arrarr-callret-param-embed-safe-wasm", `function mk2(s: string): string[][] {
    return [[s], ["c" + "d"]];
}
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s1: string = "aa" + "bb";
        var g: string[][] = mk2(s1);
        if (g[0][0].len() != 4) { bad = 1; }
        if (s1.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"arrarr-fnscope-sweep-flat-wasm", `function work(n: i32): i32 {
    var g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
    return g.len() + g[0][0].len() + n;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1500) { acc = acc + work(j); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
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
				t.Errorf("arrarr-callret wasm IR %q = %d, want %d (98 = leaked; 99 = over-release; 88 = live value freed; 97 = corrupted)", tc.name, got, tc.expected)
			}
		})
	}
}
