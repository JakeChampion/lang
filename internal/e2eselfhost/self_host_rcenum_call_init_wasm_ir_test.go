package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcEnumCallInitWasmIR is the wasm port of
// TestSelfHostRcEnumCallInitIRX86_64 (#4355 slice 5): the RCENUM call-init
// admission on the wasm IR path. The wasm $__struct_drop_<T> body already
// returned the box correctly ((local.get $box) tail — no register to
// clobber), so only the admission widening is new here; on wasm a heap
// string is one inline rc-headered block ($__fern_arr_dec IS the string
// free), so the string-field payload chain reclaims fully.
func TestSelfHostRcEnumCallInitWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host rcenum-call-init wasm IR e2e")
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
		// CALL-INIT, STRING-field payload struct — churn flat, detector zero.
		{"rcenum-call-init-str-flat-wasm", `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function main(): i32 {
    var base: string = "a";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var e: E = mk(base, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 1500) {
        var e2: E = mk(base, j);
        match (e2) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
		// PARAM-EMBED exclusion pin.
		{"rcenum-call-init-param-embed-safe-wasm", `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm, n: n }, n); }
function main(): i32 {
    var keep: string = "aa" + "bb";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var e: E = mk(keep, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (keep.len() != 4) { return 88; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
		// Struct-payload consume at detector zero (parity pin with the natives'
		// return-clobber regression case).
		{"rcenum-struct-payload-detector-zero-wasm", `struct Inner { items: i32[] }
enum Box { Full(Inner), Empty }
function readit(): i32 {
    var b: Box = Full(Inner { items: [1,2,3,4] });
    var r: i32 = 0;
    match (b) { Full(inner) => { r = inner.items[0]; }, Empty => { r = 0; } }
    return r;
}
function main(): i32 {
    var s: i32 = 0;
    var f: i32 = 0;
    while (f < 1500) { s = s + readit(); f = f + 1; }
    if (s != 1500) { return 97; }
    return __rc_underflow();
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
				t.Errorf("rcenum-call-init wasm IR %q = %d, want %d (98 = chain leaked; 99 = underflow/over-release; 88 = live value freed; 97 = value corrupted)", tc.name, got, tc.expected)
			}
		})
	}
}
