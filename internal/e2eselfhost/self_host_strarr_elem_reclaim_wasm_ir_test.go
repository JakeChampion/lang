package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStrArrElemReclaimWasmIR is the wasm port of the #4355 string[]
// ELEMENT reclaim (x86 sibling: TestSelfHostStrArrElemReclaimIRX86_64). On wasm
// __fern_str_arr_free maps to $__fern_arr_dec_ptr — at rc==1 it $__fern_arr_dec's
// every element (which IS the wasm string free: a wasm heap string is a single
// inline rc-headered block) then frees the buffer. The reclaim is proven by
// CORRECTNESS + the over-release detector ($__fern_rc_underflow → exit 99) over
// a bounded churn, plus a WAT-shape assertion on the $__fern_arr_dec_ptr call —
// present when the SARR gate admits the array, absent when the element-hazard
// walk excludes it. Heap-exhaustion churn is left to the x86 path.
func TestSelfHostStrArrElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string[] element reclaim wasm IR e2e")
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
		wantCall string // "yes" = $__fern_arr_dec_ptr call must be present, "no" = absent
	}{
		// RECLAIM: fresh-element string[] (literal + concats, sanctioned
		// self-append), 20000 build/drop cycles. Every element box is freed
		// exactly once per exit sweep — a double free ticks the underflow
		// detector → 99. lens 3+3+4 = 10 → bad stays 0 → exit 0.
		{"strarr-elem-reclaim-churn-wasm", `function build(pre: string): i32 { var xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); var tl: i32 = 0; var j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(20000); if (__rc_underflow() != 0) { return 99; } return v; }`, 0, "yes"},
		// BOUNDED HIGH-WATER: after a 3000-iteration warmup, a second churn(3000)
		// re-serves every allocation from the freelist — the bump high-water
		// stays flat (< 256 B slack). Element leaks would grow it per iteration
		// → 98; a double-free ticks the underflow detector → 99.
		{"strarr-elem-reclaim-flat-wasm", `function build(pre: string): i32 { var xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); var tl: i32 = 0; var j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0, "yes"},
		// LOOP-BODY REINIT, BOUNDED HIGH-WATER (#4353 item 4): a string[]
		// re-DECLARED each iteration is freed at the loop REBIND
		// (emit_strarr_reclaim_store), not at a helper exit. After a 3000-iter
		// warmup the second churn re-serves from the freelist → flat bump.
		// Pre-fix the reinit store leaked all 3 element boxes per iteration → 98.
		{"strarr-elem-reinit-loop-wasm", `function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var xs: string[] = ["lit", pre + "x", pre + "yy"]; acc = (acc + xs[0].len() + xs[2].len()) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0, "yes"},
		// ELEMENT ALIAS BINDING excludes: `var t = xs[0]` — xs keeps the shallow
		// buffer-only dec; t stays valid, nothing double-frees. 3+2 = 5.
		{"strarr-elem-alias-excluded-wasm", `function pick(pre: string): i32 { var xs: string[] = [pre + "x", "qq"]; var t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`, 0, "no"},
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
			// Count a $__fern_arr_dec_ptr call only inside a USER function:
			// since #4355 slice 9 the runtime's $__fern_arr_dec_ptr2 body
			// itself calls $__fern_arr_dec_ptr per inner (and the name is a
			// substring match too), so a whole-WAT Contains misreads every
			// compile as "admitted". Track the enclosing (func $name ...)
			// and skip runtime ($__*) bodies; require a non-ident char after
			// the helper name so $__fern_arr_dec_ptr2 never matches.
			hasCall := false
			inUser := false
			for _, ln := range strings.Split(string(wat), "\n") {
				if i := strings.Index(ln, "(func $"); i >= 0 {
					inUser = !strings.HasPrefix(ln[i+len("(func $"):], "__")
					continue
				}
				if !inUser {
					continue
				}
				if j := strings.Index(ln, "call $__fern_arr_dec_ptr"); j >= 0 {
					rest := ln[j+len("call $__fern_arr_dec_ptr"):]
					if rest == "" || (rest[0] != '2' && rest[0] != '_') {
						hasCall = true
						break
					}
				}
			}
			if tc.wantCall == "yes" && !hasCall {
				t.Fatalf("%s: emitted WAT has no $__fern_arr_dec_ptr call — the string[] was not admitted to the SARR reclaim set", tc.name)
			}
			if tc.wantCall == "no" && hasCall {
				t.Fatalf("%s: emitted WAT calls $__fern_arr_dec_ptr — the element-hazard walk failed to exclude an aliased element", tc.name)
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
				t.Errorf("string[] element reclaim wasm IR %q = %d, want %d (99 = double-free detected)", tc.name, got, tc.expected)
			}
		})
	}
}
