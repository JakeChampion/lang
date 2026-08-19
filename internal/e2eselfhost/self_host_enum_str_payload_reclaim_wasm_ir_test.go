package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostEnumStrPayloadReclaimWasmIR is the wasm port of the #4355 slice-2
// enum/Option STRING-payload release (x86 sibling:
// TestSelfHostEnumStrPayloadReclaimIRX86_64). On wasm __fern_str_free maps to
// $__fern_arr_dec (a wasm heap string is one inline rc-headered block), so the
// same IR-level payload drops release the payload there. Bounded high-water
// (__heap_bump_bytes flat across a second churn) + the over-release detector
// (__rc_underflow → 99) + values, on the -ir driver path.
func TestSelfHostEnumStrPayloadReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host enum string-payload reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// ENUM string payload consumed by match — flat across the second churn.
		{"enum-str-payload-flat-wasm", `enum Tok { Word(string), Num(i32) }
function go(pre: string): i32 { var x = Word(pre + "abc"); var r = 0; match (x) { Word(s) => { r = s.len(); }, Num(n) => { r = n; }, } return r; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		// OPTION string payload consumed by match — flat.
		{"option-str-payload-flat-wasm", `function go(pre: string): i32 { var o: Option[string] = Some(pre + "xyz"); var r = 0; match (o) { Some(s) => { r = s.len(); }, None => { r = 1; }, } return r; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		// RESULT Err string payload — flat.
		{"result-err-str-payload-flat-wasm", `function go(pre: string): i32 { var r2: Result[i32, string] = Err(pre + "e"); var r = 0; match (r2) { Ok(v) => { r = v; }, Err(e) => { r = e.len(); }, } return r; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		// NON-FRESH payload excluded — nm stays valid, detector 0.
		{"option-aliased-str-payload-excluded-wasm", `function go(pre: string): i32 { var nm: string = pre + "q"; var o: Option[string] = Some(nm); var r = 0; match (o) { Some(s) => { r = s.len(); }, None => { r = 1; }, } return r + nm.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (go(pre) != 6) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`, 0},
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
				t.Errorf("enum/option string payload wasm IR %q = %d, want %d (98 = payload leaked; 99 = double-free)", tc.name, got, tc.expected)
			}
		})
	}
}
