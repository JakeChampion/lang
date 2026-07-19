package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedTupleCaptureIRCases exercise a lambda capturing a binding produced by a
// NESTED tuple destructure — a first destructure binds a name to a tuple
// (pointer) element, a second destructure splits that — through the self-host IR
// path on x86-64.
//
// #5173 taught the lift's cap_type resolver to read a destructure binding's
// element type, but only for SCALAR/string element tags: a first-level
// destructure whose element is itself a tuple (`var (p, c) = t` with
// t : ((i32,i32), i32), so p : (i32,i32)) resolved "", so the second-level
// `var (a, b) = p` couldn't resolve p's tuple type and the capture of a/b
// declined the lift, dropping the module to the (miscompiling) AST emitter
// (#5201). The fix returns the tuple (pointer) element tag — gated to all-i32
// tuples, whose element reads capture soundly via the 32-bit env slot — so the
// nested-destructure capture (and a direct capture of such a tuple binding)
// lift. A pointer-element (string/…) destructure binding stays declined: that is
// a SEPARATE pre-existing nested-destructure lowering gap, out of scope here.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; results stay <= 120 (the wasm exit-code clamp, #2908).
var nestedTupleCaptureIRCases = []struct {
	name string
	main string
}{
	// The #5201 repro: two-level all-i32 nesting, innermost bindings captured in
	// a struct fn-field lambda. h.f(1)=1+3+4+5=13, h.id=a=3 → 16.
	{"nested-2level", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	var t: ((i32, i32), i32) = ((3, 4), 5);
	var (p, c) = t;
	var (a, b) = p;
	var h: H = H { f: function (x: i32): i32 { return x + a + b + c; }, id: a };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	// Direct capture of the intermediate tuple binding p (a (i32,i32) pointer
	// riding the 32-bit env slot). h.f(1)=1+3+4+5=13, h.id=c=5 → 18.
	{"direct-tuple-capture", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	var t: ((i32, i32), i32) = ((3, 4), 5);
	var (p, c) = t;
	var h: H = H { f: function (x: i32): i32 { return x + p.0 + p.1 + c; }, id: c };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	// Three-level all-i32 nesting. h.f(1)=1+1+2+3+4=11, h.id=a=1 → 12.
	{"nested-3level", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	var t: (((i32, i32), i32), i32) = (((1, 2), 3), 4);
	var (q, d) = t;
	var (p, c) = q;
	var (a, b) = p;
	var h: H = H { f: function (x: i32): i32 { return x + a + b + c + d; }, id: a };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
}

// TestSelfHostNestedTupleCaptureIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostNestedTupleCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedTupleCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
