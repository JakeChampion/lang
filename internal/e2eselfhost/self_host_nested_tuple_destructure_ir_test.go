package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedTupleDestructureIRCases exercise a NESTED tuple destructure — a first
// destructure binds a name to a tuple (pointer) element, a second destructure
// (or a `.N` read) splits that — through the self-host IR path on x86-64.
//
// The destructure lowering marked a bound slot's element kind (string / i64 /
// array / struct / …) for scalar and pointer LEAF elements, but had no branch
// for a nested-TUPLE element: `var (p, c) = t` with t : ((string,i32), i32)
// bound p (a (string,i32) pointer) with NO tuple_elems, so the second-level
// `var (s, b) = p` resolved s's tag as "" and read it via the untyped
// op_tuple_get — a pointer element (string) came through empty (#5306 gap 1).
// The fix records the nested tuple's element tags on p's slot, so the second
// destructure (and `p.N`) resolve each element's type. i32 nested elements were
// already correct (they need no per-element mark); this closes the pointer case.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; results stay <= 120 (the wasm exit-code clamp, #2908).
var nestedTupleDestructureIRCases = []struct {
	name string
	main string
}{
	// Nested string element via a second destructure: 2 + 4 + 5 = 11 (was 9 — s
	// came through empty).
	{"nested-str-2level", `function g(): i32 {
	var t: ((string, i32), i32) = (("hi", 4), 5);
	var (p, c) = t;
	var (s, b) = p;
	return s.len() + b + c;
}
function main(): i32 { return g(); }`},
	// Nested string element read via `.N` on the intermediate binding: 3+4+5 = 12.
	{"nested-str-dotN", `function g(): i32 {
	var t: ((string, i32), i32) = (("hey", 4), 5);
	var (p, c) = t;
	return p.0.len() + p.1 + c;
}
function main(): i32 { return g(); }`},
	// Three-level nesting with a string at the bottom: 2 + 1 + 2 + 3 = 8.
	{"nested-str-3level", `function g(): i32 {
	var t: (((string, i32), i32), i32) = ((("ab", 1), 2), 3);
	var (q, d) = t;
	var (p, c) = q;
	var (s, b) = p;
	return s.len() + b + c + d;
}
function main(): i32 { return g(); }`},
	// All-i32 nested destructure (regression baseline): 3 + 4 + 5 = 12.
	{"nested-i32-2level", `function g(): i32 {
	var t: ((i32, i32), i32) = ((3, 4), 5);
	var (p, c) = t;
	var (a, b) = p;
	return a + b + c;
}
function main(): i32 { return g(); }`},
}

// TestSelfHostNestedTupleDestructureIRX86_64 routes each case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostNestedTupleDestructureIRX86_64(t *testing.T) {
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

	for _, tc := range nestedTupleDestructureIRCases {
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
