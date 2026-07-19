package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tupleDestructureStrIRCases pin tuple destructuring (`var (a, b) = E` /
// `let (a, b) = E`) where at least one element is a POINTER-shaped value
// (`string`) on the self-host IR path (x86-64 + wasm). The existing
// tuple-destructure pin (self_host_tuple_destructure_ir_test) deliberately stays
// scalar `(i32, i32)` ("the confirmed-lowering shape"); these cases extend the
// coverage to mixed scalar+pointer and all-pointer tuples, plus a 3-element
// tuple — exercising the `tuple_get` element reads at pointer width for the
// string slots (and the i32 slots in the same tuple). All already lower, so no
// compiler change — an observability pin against a regression to the AST
// fallback.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; results stay <= 120 (the wasm exit-code clamp, #2908).
const tupleDestructureStrIRPrelude = `function mk2(): (i32, string) { return (5, "hi"); }
function mkStrFirst(): (string, i32) { return ("abc", 9); }
function mk3(): (i32, string, i32) { return (1, "xy", 2); }
function mk2str(): (string, string) { return ("ab", "cde"); }
function mkNest(): ((string, i32), i32) { return (("hi", 4), 5); }
`

var tupleDestructureStrIRCases = []struct {
	name string
	main string
	want int
}{
	// scalar + string element: 5 + len("hi") = 7.
	{"scalar-then-str", `var (a, s) = mk2(); return a + s.len();`, 7},
	// string-first tuple: len("abc") + 9 = 12.
	{"str-then-scalar", `var (s, n) = mkStrFirst(); return s.len() + n;`, 12},
	// three-element mixed tuple: 1 + len("xy") + 2 = 5.
	{"three-mixed", `var (a, s, b) = mk3(); return a + s.len() + b;`, 5},
	// both elements pointer-shaped: len("ab") + len("cde") = 5.
	{"two-strings", `var (p, q) = mk2str(); return p.len() + q.len();`, 5},
	// the `let` binder form with a string element: 5 + len("hi") = 7.
	{"let-scalar-str", `let (a, s) = mk2(); return a + s.len();`, 7},
	// bind only the string element (the i32 slot is still read past).
	{"str-only-use", `var (a, s) = mk2(); return s.len();`, 2},
	// NESTED string destructure (#5306 Gap 1): `var (p, c) = t` binds
	// p : (string, i32), then `var (s, b) = p` reads the string element. Before
	// the fix the second-level read got an untyped slot (the destructure-bound
	// p carried no inner tuple tags), so s.len() read 0 → 9 instead of 11. The
	// fix records p's inner element tags (mark_tuple_elems) at the first bind.
	// len("hi") + 4 + 5 = 11.
	{"nested-str-destructure", `var (p, c) = mkNest(); var (s, b) = p; return s.len() + b + c;`, 11},
	// The same nested shape from a tuple LITERAL local (not a call): exercises
	// the ExprTuple init tag path feeding the inner-tuple tag record.
	{"nested-str-literal", `var t: ((string, i32), i32) = (("yo", 3), 6); var (p, c) = t; var (s, b) = p; return s.len() + b + c;`, 11},
}

func tupleDestructureStrIRSrc(mainBody string) string {
	return tupleDestructureStrIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTupleDestructureStrIRX86_64 routes each case through the
// self-hosted x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostTupleDestructureStrIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
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

	for _, tc := range tupleDestructureStrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tupleDestructureStrIRSrc(tc.main))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTupleDestructureStrIRWasm runs the same cases through the wasm IR
// backend (where the pointer-width tuple element reads use wasm32's 4-byte
// pointer slot).
func TestSelfHostTupleDestructureStrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-destructure-str wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range tupleDestructureStrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tupleDestructureStrIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "tuple_destructure_str_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("tuple-destructure-str wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
