package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructArrReclaimWasmIR is the wasm port of
// TestSelfHostStructArrReclaimIRX86_64 (#4355 struct-array element-box reclaim).
// __fern_arrarr_free maps to the existing $__fern_arr_dec_ptr on wasm, which at
// rc==1 $__fern_arr_dec's every element pointer (exactly the wasm struct-box
// free — __fern_rc_dec also maps to $__fern_arr_dec) then frees the outer
// buffer. So a fresh, non-escaping `var g = [P { .. }, P { .. }]` reclaims its
// element boxes with no new wasm runtime.
func TestSelfHostStructArrReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host structarr-reclaim wasm IR e2e")
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
		{"structarr-scalar-flat-wasm", `struct P { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        acc = acc + g.len() + g[0].x;
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 2000) {
        var g2 = [P { x: j, y: j + 1 }, P { x: j + 2, y: j + 3 }];
        acc = acc + g2.len() + g2[1].y;
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		{"structarr-iter-flat-wasm", `struct P { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        for p in g { acc = acc + p.x + p.y; }
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 2000) {
        var g2 = [P { x: j, y: j + 1 }, P { x: j + 2, y: j + 3 }];
        for p in g2 { acc = acc + p.x; }
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		{"structarr-elem-alias-safe-wasm", `struct P { x: i32, y: i32 }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        var q = g[1];
        if (q.x != i + 2) { bad = 1; }
        if (q.y != i + 3) { bad = 1; }
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
				t.Errorf("structarr-reclaim wasm IR %q = %d, want %d (98 = element boxes leaked; 99 = over-release; 88 = live value freed; 97 = corrupted)", tc.name, got, tc.expected)
			}
		})
	}
}
