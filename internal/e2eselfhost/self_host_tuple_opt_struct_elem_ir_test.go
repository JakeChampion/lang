package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A tuple element that is a DIRECT `Some(<struct>)` construction — the shape
// `coreutils/lib/gnu.fern`'s getopt cursor returns from `next` /
// `short_option` / `long_option`:
//
//	function (g: Getopt) next(): (Option[OptMatch], Getopt)
//
// The self-host tuple-literal admission took an Option element only when its
// payload was a scalar, a string or a closure, so this bailed the WHOLE module
// ("did not lower: tuple literal") and no coreutils utility declaring an option
// could be compiled by the self-host compiler at all (#8407).
//
// What makes the widening sound is that the same runtime shape was already
// being built two other ways. An Option[S] LOCAL as an element, and a CALL
// returning Option[S] as an element, both lowered before this change — the
// payload box is one pointer in the slot at offset 8, which is exactly what the
// match lowering's struct-payload branch reads. Only the spelling that names
// `Some` at the element position was refused, so `(pick(5), g)` lowered while
// `(Some(S { … }), g)` refused the module. The controls below are those two
// spellings: they must keep lowering, and all three must agree with the interp
// oracle.
//
// The payload struct still has to be leak-safe (opt_payload_struct_ok), the
// same requirement a struct FIELD of type `Option[S]` has carried since
// is_leaksafe_opt_field_d admitted it.
//
// The enum cases below are the same gap one payload kind over, found by wc's
//
//	function count_stream(r: Reader, show: Show): (Counts, Option[IoError])
//
// which is the shape every streaming utility returns an IO error through, so
// the whole of coreutils group B was blocked behind it (#8737). A nominal enum
// payload is the same one-pointer-at-offset-8 slot the struct payload is, and
// the match lowering's ptag_is_enum branch already read it — only the
// construction tag was missing, so `(c, Some(e))` refused the module while a
// BARE enum element and `var o: Option[E] = Some(e)` both lowered. Those two
// spellings are the controls.
var tupleOptStructElemCases = []struct {
	name string
	src  string
}{
	// The cursor shape itself: an Option[struct] beside a struct, both read.
	{"some_struct_literal", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function step(g: G, k: i32): (Option[S], G) {
    if (k > 0) { return (Some(S { id: k, v: "hit" }), G { ...g, i: g.i + k }); }
    return (None, G { ...g, i: g.i + 1 });
}
function main(): i32 {
    var g: G = G { i: 0, name: "n" };
    var r: (Option[S], G) = step(g, 5);
    var a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id + s.v.len(); }, None => { a = a + 100; } }
    var r2: (Option[S], G) = step(r.1, 0);
    match (r2.0) { Some(s2) => { a = a + 100; }, None => { a = a + r2.1.i; } }
    return a;
}`},
	// The payload is a struct LOCAL rather than a literal.
	{"some_struct_ident_payload", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function f(g: G): (Option[S], G) {
    var p: S = S { id: 5, v: "hit" };
    return (Some(p), G { ...g, i: 9 });
}
function main(): i32 {
    var r: (Option[S], G) = f(G { i: 0, name: "n" });
    var a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 100; } }
    return a;
}`},
	// Option[struct] beside a SCALAR, so the widened tag is not carried by a
	// neighbouring struct element.
	{"some_struct_scalar_pair", `struct S { id: i32, v: string }
function f(): (Option[S], i32) { return (Some(S { id: 5, v: "hit" }), 4); }
function main(): i32 {
    var r: (Option[S], i32) = f();
    var a: i32 = r.1;
    match (r.0) { Some(s) => { a = a + s.id + s.v.len(); }, None => { a = a + 100; } }
    return a;
}`},
	// Controls — the two spellings that already lowered.
	{"fn_call_option_struct_control", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function pick(k: i32): Option[S] {
    if (k > 0) { return Some(S { id: k, v: "hit" }); }
    return None;
}
function f(g: G): (Option[S], G) { return (pick(5), G { ...g, i: 9 }); }
function main(): i32 {
    var r: (Option[S], G) = f(G { i: 0, name: "n" });
    var a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 100; } }
    return a;
}`},
	{"option_struct_local_control", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function f(g: G): (Option[S], G) {
    var o: Option[S] = Some(S { id: 5, v: "hit" });
    return (o, G { ...g, i: 9 });
}
function main(): i32 {
    var r: (Option[S], G) = f(G { i: 0, name: "n" });
    var a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 100; } }
    return a;
}`},
	{"none_control", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function f(g: G): (Option[S], G) { return (None, G { ...g, i: 9 }); }
function main(): i32 {
    var r: (Option[S], G) = f(G { i: 0, name: "n" });
    var a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 3; } }
    return a;
}`},
	{"some_scalar_control", `struct G { i: i32, name: string }
function f(g: G): (Option[i32], G) { return (Some(7), G { ...g, i: 9 }); }
function main(): i32 {
    var r: (Option[i32], G) = f(G { i: 0, name: "n" });
    var a: i32 = r.1.i;
    match (r.0) { Some(x) => { a = a + x; }, None => { a = a + 100; } }
    return a;
}`},
}

// TestSelfHostTupleOptStructElemIRX86_64 — the x86-64 IR path. Every case runs
// under FERN_STRICT_IR=1, so a per-function bail refuses the build rather than
// being absorbed: that is the regression signal here, since a bail and a
// correct answer are not distinguishable from the exit code alone.
func TestSelfHostTupleOptStructElemIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleOptStructElemCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostTupleOptStructElemIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleOptStructElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleOptStructElemCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostTupleOptStructElemWasmIR — the wasm leg, where the element's kind
// tag also picks the store WIDTH, so a tag the backend does not recognise is
// not merely a bail but a differently-shaped slot.
func TestSelfHostTupleOptStructElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple Option[struct] element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupleOptStructElemCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "optstruct_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

// A METHOD returning Option[S] at a tuple element has no registry key to name
// its payload, so its tag comes from the checker's stamped result type — the
// second gate that carried the same scalar-only payload restriction, and the
// spelling `Getopt.next`'s callers use. It runs on the module-LOADING compiler
// rather than the `-ir` driver above, because only that path carries the
// stamp; under the driver the element is unclassifiable for want of a type,
// which is a different gap from this one.
const tupleOptStructStampedSrc = `struct S { id: i32, v: string }
struct G { i: i32, name: string }
struct Tab { n: i32 }
function (t: Tab) find(k: i32): Option[S] {
    if (k > 0) { return Some(S { id: k + t.n, v: "hit" }); }
    return None;
}
function f(g: G, t: Tab): (Option[S], G) { return (t.find(5), G { ...g, i: 9 }); }
function main(): i32 {
    var r: (Option[S], G) = f(G { i: 0, name: "n" }, Tab { n: 2 });
    var a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 100; } }
    return a;
}`

func TestSelfHostTupleOptStructElemStampedX86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)
	want := interpExit(t, interpBin, tupleOptStructStampedSrc)

	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(tupleOptStructStampedSrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
	if derr != nil {
		t.Fatalf("route decide: %v", derr)
	}
	if got := strings.TrimSpace(string(route)); got != "ir" {
		t.Fatalf("routed %q, want \"ir\" — the case no longer exercises the IR path", got)
	}
	asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
	if cerr != nil || len(asm) == 0 {
		t.Fatalf("loader compile: %v (%d bytes)", cerr, len(asm))
	}
	progBin := buildBin(t, gcc, dir, "tupleoptstruct_stamped", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("method-call Option[struct] element = %d, want %d (interp oracle)", got, want)
	}
}

// An Option[ENUM] element is the same gap one payload kind over, and it is the
// shape every streaming utility returns an IO error through:
//
//	function count_stream(r: Reader, show: Show): (Counts, Option[IoError])
//
// so the whole of coreutils group B was blocked behind it (#8737). A nominal
// enum payload is the same one-pointer-at-offset-8 slot the struct payload is,
// and the match lowering's ptag_is_enum branch already read it — only the
// CONSTRUCTION tag was missing, so `(c, Some(e))` refused the module while a
// bare enum element and `var o: Option[E] = Some(e)` both lowered.
//
// It is pinned on the module-LOADING compiler rather than the `-ir` driver
// above, and that distinction is the whole test: under the driver these cases
// pass WITHOUT the fix, because the payload has no stamped type there and
// elem_type_tag falls to its i32 default instead of naming the enum. Only the
// loading path resolves `e` to its enum type, reaches the admission, and
// bailed. A driver-path case would have looked like coverage and asserted
// nothing.
const tupleOptEnumStampedSrc = `enum E { A, B(i32) }
struct C { n: i32 }
function step(c: C, k: i32): (C, Option[E]) {
    var e: E = B(k);
    if (k > 0) { return (C { n: c.n + k }, Some(e)); }
    return (C { n: c.n + 1 }, None);
}
function main(): i32 {
    var r: (C, Option[E]) = step(C { n: 0 }, 5);
    var a: i32 = r.0.n;
    match (r.1) { Some(x) => { match (x) { A => { a = a + 50; }, B(v) => { a = a + v; } } }, None => { a = a + 100; } }
    var r2: (C, Option[E]) = step(r.0, 0);
    match (r2.1) { Some(y) => { a = a + 100; }, None => { a = a + r2.0.n; } }
    return a;
}`

func TestSelfHostTupleOptEnumElemStampedX86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)
	want := interpExit(t, interpBin, tupleOptEnumStampedSrc)

	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(tupleOptEnumStampedSrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
	if derr != nil {
		t.Fatalf("route decide: %v", derr)
	}
	if got := strings.TrimSpace(string(route)); got != "ir" {
		t.Fatalf("routed %q, want \"ir\" — the case no longer exercises the IR path", got)
	}
	asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
	if cerr != nil || len(asm) == 0 {
		t.Fatalf("loader compile: %v (%d bytes)", cerr, len(asm))
	}
	progBin := buildBin(t, gcc, dir, "tupleoptenum_stamped", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("Option[enum] tuple element = %d, want %d (interp oracle)", got, want)
	}
}
