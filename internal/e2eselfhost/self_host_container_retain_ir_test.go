package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The construction-store retain at the three container constructors, on the IR
// path (#3457).
//
// Storing a local into an array literal, a tuple literal, or an Option/Result
// payload makes the container a SECOND owner of that heap value, so it must be
// retained. All three sites already did this — for ARRAY slots only. A string or
// tuple local kept rc 1, and `__fern_rc_is_unique` then called it unique while the
// container still pointed at it. That is not just a wrong observation: uniqueness
// is what the constructor-reuse guard (#4350) asks before writing in place, so a
// container-retained value reported unique is a reuse hazard. The three sites now
// share one predicate, `slot_is_rc_container`, which is what stops them drifting
// apart again.
//
// These were invisible because the RC corpus reaches the counter through
// `__fern_rc_underflow_count()`, a spelling only the AST emitters accepted — so ~15
// RC test files were exercising the AST emitter's discipline and never the IR
// path's. irlower now accepts both spellings, which is what routes them here.
func TestSelfHostContainerRetainIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// Each case reads __fern_rc_is_unique of a local AFTER storing it into a
	// container: 0 (shared) is the correct answer and the one both AST emitters
	// give. The value is also read back through the container, so a retain that
	// broke the store would show up as a wrong sum rather than a passing 0.
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// ARRAY slot — the kind that already worked, kept as the control.
		{"array-into-tuple", `function main(): i32 { var xs: i32[] = [1, 2, 3]; var t = (xs, 99); return __fern_rc_is_unique(xs) + t.0[2]; }`, 3},
		// STRING slots at all three constructors.
		{"string-into-tuple", `function main(): i32 { var s: string = "x" + "yz"; var t = (s, 99); return __fern_rc_is_unique(s) + t.0.len(); }`, 3},
		{"string-into-array", `function main(): i32 { var a: string = "p" + "q"; var arr: string[] = [a]; return __fern_rc_is_unique(a) + arr[0].len(); }`, 2},
		{"string-into-option", `function main(): i32 { var s: string = "ab" + "cd"; var o = Some(s); return __fern_rc_is_unique(s) + s.len(); }`, 4},
		// TUPLE slots — a tuple box stored into another container.
		{"tuple-into-array", `function main(): i32 { var t = (3, 4); var arr = [t]; return __fern_rc_is_unique(t) + arr[0].0; }`, 3},
		{"tuple-into-tuple", `function main(): i32 { var t = (5, 6); var o = (t, 99); return __fern_rc_is_unique(t) + o.0.1; }`, 6},
		// A FRESH literal element is not a local: it is moved, not retained, so
		// the deep-free paths that own fresh elements stay correct.
		{"fresh-element-not-retained", `function main(): i32 { var t = ([1, 2, 3], 99); return t.0[2] + 4; }`, 7},
		// The RC counters are read by a module that allocates NOTHING an op
		// reports (a scalar-capture closure). rc_runtime_helpers rides the heap
		// gate, so without pulling it in for a counter read the emitted core
		// called a function it never defined.
		{"scalar-closure-reads-counter", `function main(): i32 { var n: i32 = 5; var f = function (x: i32): i32 { return x + n; }; return f(37) + __fern_rc_underflow_count(); }`, 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — the case is not exercising the IR path it is about", tc.name, route)
			}
			wat := runCapture(t, gcc, runner, driverBin, src)
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if werr := os.WriteFile(watPath, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			cmd := exec.Command(wasmtime, "run", watPath)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d — a container-stored value reported unique\n%s", tc.name, code, tc.exit, out)
			}
		})
	}
}
