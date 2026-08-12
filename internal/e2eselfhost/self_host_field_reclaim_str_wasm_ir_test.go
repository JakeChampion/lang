package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostFieldReclaimStrWasmIR is the wasm port of the #4355
// replaced-STRING-field reclaim (x86 sibling:
// TestSelfHostFieldReclaimStrIRX86_64). On wasm a heap string is one inline
// rc-headered block, so $__fern_arr_dec IS the string free — the widened
// $__field_reclaim_<T> body decs a replaced string field under the same
// cow + snap guards as the array fields.
func TestSelfHostFieldReclaimStrWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host field-reclaim-str wasm IR e2e")
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
		// Consume-rebind churn — the replaced string field recycles, flat.
		{"field-reclaim-str-flat-wasm", `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n, s.n + 1], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    var s: S = S { xs: [1, 2], name: "a" + "b", n: 0 };
    var i: i32 = 0;
    while (i < 200) { s = step(s); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1500) { s = S { xs: [1, 2], name: "a" + "b", n: 0 }; var k: i32 = 0; while (k < 3) { s = step(s); k = k + 1; } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (s.n != 3) { return 97; }
    return 0;
}`, 0},
		// Carried field (functional update) stays readable, no double-free.
		{"field-reclaim-str-carried-safe-wasm", `struct S { xs: i32[], name: string, n: i32 }
function bump(s: S): S { return S { ...s, n: s.n + 1 }; }
function main(): i32 {
    var s: S = S { xs: [1, 2], name: "ab" + "cd", n: 0 };
    var i: i32 = 0;
    while (i < 1500) { s = bump(s); i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (s.name.len() != 4) { return 97; }
    if (s.n != 1500) { return 96; }
    return 0;
}`, 0},
		// STRING-ONLY struct (#4355 slice 3) — routed via STRFLDOK, churn flat.
		{"field-reclaim-str-only-flat-wasm", `struct B { name: string, n: i32 }
function step(b: B): B { return B { name: b.name + "x", n: b.n + 1 }; }
function main(): i32 {
    var b: B = B { name: "a" + "b", n: 0 };
    var i: i32 = 0;
    while (i < 200) { b = step(b); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1500) { b = B { name: "a" + "b", n: 0 }; var k: i32 = 0; while (k < 3) { b = step(b); k = k + 1; } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (b.n != 3) { return 97; }
    return 0;
}`, 0},
		// Aliased read (`var t = s.name`) survives the rebind's field free.
		{"field-reclaim-str-aliased-read-safe-wasm", `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s: S = S { xs: [1], name: "a" + "b", n: 0 };
        var t: string = s.name;
        s = step(s);
        s = step(s);
        if (t.len() != 2) { bad = 1; }
        if (s.name.len() != 4) { bad = 1; }
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
				t.Errorf("field-reclaim-str wasm IR %q = %d, want %d (98 = string field leaked; 99 = double-free; 97/96 = value corrupted; 88 = aliased read freed)", tc.name, got, tc.expected)
			}
		})
	}
}
