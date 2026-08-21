package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateStructCases extend the typed-IR annotation (#5531) to struct-valued
// calls. type_to_irtag now emits a TypeStruct's NAME as the tag, and
// expr_struct_type's ExprCall arm reads it (when it names a known struct)
// instead of re-deriving via the struct_ret_type registry. Each program uses a
// struct-returning call's result for a FIELD or METHOD access directly on the
// call (so expr_struct_type on the call is the code path under test), oracle-
// checked against the interpreter.
var annotateStructCases = []struct {
	name string
	src  string
}{
	// field access on a struct-returning call result.
	{"call_field", `struct P { x: i32, y: i32 }
function mk(a: i32): P { return P { x: a, y: a * 2 }; }
function main(): i32 { return mk(7).x + mk(3).y; }`}, // 7 + 6 = 13
	// method call on a struct-returning call result.
	{"call_method", `struct P { x: i32, y: i32 }
function (p: P) sum(): i32 { return p.x + p.y; }
function mk(a: i32): P { return P { x: a, y: a + 1 }; }
function main(): i32 { return mk(10).sum(); }`}, // 10 + 11 = 21
	// struct-returning call bound to an inferred local, then field read.
	{"call_infer_local", `struct Pt { x: i32, y: i32 }
function origin(): Pt { return Pt { x: 3, y: 4 }; }
function main(): i32 { var p = origin(); return p.x * p.y; }`}, // 12
}

// TestSelfHostAnnotateStructIR_X86_64 pins the checker-stamped struct result type
// feeding irlower's expr_struct_type through the IR path (#5531).
func TestSelfHostAnnotateStructIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)
	for _, tc := range annotateStructCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}
			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annstruct_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
