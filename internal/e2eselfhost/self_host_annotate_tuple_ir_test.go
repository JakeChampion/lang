package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateTupleCases extend the typed-IR annotation (#5531) to tuple-valued
// calls. type_to_irtag now serialises a TypeTuple to its canonical
// "(t0, t1, …)" tag, and expr_tuple_elem_tag gained an ExprCall arm that reads
// it. This closes a genuine coverage gap: a `f().N` read of a STRUCT- or
// nested-tuple-typed element of a tuple-returning call had no tag at the call
// receiver, so expr_struct_type(f().N) was "" and `f().N.field` / `f().N.m()`
// bailed the whole function. With the annotation those
// functions route through the IR path (unlike the byte-identical earlier
// slices, this WIDENS IR routing — verified by the interpreter oracle, and by
// the `-decide` route being "ir": these programs are not structurally IR-
// eligible, so `-decide` — which now annotates, mirroring emit_module — is the
// canary that the annotate wiring is what lifts them).
var annotateTupleCases = []struct {
	name string
	src  string
}{
	// field read of a struct-typed element of a tuple-returning call.
	{"call_elem_field", `struct P { x: i32, y: i32 }
function mk(): (P, i32) { return (P { x: 5, y: 9 }, 3); }
function main(): i32 { return mk().0.y; }`}, // 9
	// method call on a struct-typed element of a tuple-returning call.
	{"call_elem_method", `struct P { x: i32, y: i32 }
function (p: P) s(): i32 { return p.x + p.y; }
function mk(): (P, i32) { return (P { x: 4, y: 6 }, 1); }
function main(): i32 { return mk().0.s(); }`}, // 10
	// two struct-typed elements of a tuple-returning call.
	{"call_elem_both", `struct P { x: i32, y: i32 }
function mk(): (P, P) { return (P { x: 1, y: 2 }, P { x: 3, y: 4 }); }
function main(): i32 { return mk().0.y + mk().1.x; }`}, // 2 + 3 = 5
}

// TestSelfHostAnnotateTupleIR_X86_64 pins the checker-stamped tuple result type
// feeding irlower's expr_tuple_elem_tag through the IR path (#5531).
func TestSelfHostAnnotateTupleIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	// Runner prefix: nil on an x86_64 host (native exec), qemu-x86_64 on an
	// aarch64 dev box. These programs allocate a heap (struct + tuple boxes via
	// arena mmap), which SIGSEGVs under binfmt-direct exec in a cross-arch
	// container but runs correctly under explicit qemu — so run via the runner.
	_, runner := x86_64Tooling(t)

	for _, tc := range annotateTupleCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "anntuple_"+tc.name, string(asm))
			argv := append(append([]string{}, runner...), progBin)
			cmd := exec.Command(argv[0], argv[1:]...)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
