package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// arrLenCallIRCases exercise calling a builtin array method (`.len()`) DIRECTLY
// on a function-call result that returns a STRUCT array (`P[]`) — no intermediate
// local. Before this, irlower's `.len()` guard consulted expr_struct_type, which
// reports the ELEMENT type "P" for a `P[]`-returning call (it strips the `[]` for
// `mk()[i].field` recovery, #3035). That made `decl_is_struct` true, so `.len()`
// was mis-routed to a (nonexistent) `P.len` user method and the whole module
// bailed the module. A struct-array LOCAL was already fine (its arr slot
// reports ""), so only the direct-call form was affected — the exact shape
// std/regex's `regex_count` (`regex_find_all(p, t).len()`) uses. The fix treats
// an array-source receiver as the builtin array length regardless of the
// element-type leak. Oracle = the native interpreter.
var arrLenCallIRCases = []struct {
	name string
	main string
}{
	// The bare gap: `.len()` on a struct-array call result.
	{"struct-arr-len", `struct S { x: i32 }
function mkS(): S[] { return [S { x: 1 }, S { x: 2 }, S { x: 3 }]; }
function main(): i32 { return mkS().len(); }`},
	// Two calls combined, to show it is not a one-shot fluke.
	{"struct-arr-len-twice", `struct S { x: i32 }
function mkS(): S[] { return [S { x: 1 }, S { x: 2 }, S { x: 3 }, S { x: 4 }]; }
function main(): i32 { return mkS().len() * 10 + mkS().len(); }`},
	// A multi-field struct element, to confirm the element layout is irrelevant
	// to reading the array header length.
	{"named-struct-arr-len", `struct Pair { a: i32, b: i32 }
function mk(): Pair[] { return [Pair { a: 1, b: 2 }, Pair { a: 3, b: 4 }]; }
function main(): i32 { return mk().len() + 40; }`},
	// Regression guard: a struct VALUE with a user-defined `.len()` method must
	// STILL dispatch to that method (not the array-length builtin) — the case the
	// original guard protected (#3478).
	{"struct-value-user-len", `struct Box { v: i32 }
function (b: Box) len(): i32 { return b.v + 100; }
function mk(): Box { return Box { v: 5 }; }
function main(): i32 { var b: Box = mk(); return b.len(); }`},
}

// TestSelfHostArrLenCallIRX86_64 builds asm_run + asm_pathprobe_run and asserts
// each case (1) routes the "ir" path and (2) exits with the interp oracle value.
func TestSelfHostArrLenCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range arrLenCallIRCases {
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
			progBin := buildBin(t, gcc, dir, "arrlencall_"+tc.name, string(asm))
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

// TestSelfHostArrLenCallIRWasm runs the same cases through the wasm IR backend —
// the `.len()` lowering lives in irlower (target-independent), so wasm gets the
// fix for free. Same interp oracle.
func TestSelfHostArrLenCallIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arr-len-call wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrLenCallIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = strings.NewReader(string(src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "alc_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("arr-len-call wasm IR %q = %d, want %d", tc.name, got, want)
			}
		})
	}
}
