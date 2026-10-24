package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var tupleOptStructElemCases = []struct {
	name string
	src  string
}{
	{"some_struct_literal", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function step(g: G, k: i32): (Option[S], G) {
    if (k > 0) { return (Some(S { id: k, v: "hit" }), G { ...g, i: g.i + k }); }
    return (None, G { ...g, i: g.i + 1 });
}
function main(): i32 {
    let g: G = G { i: 0, name: "n" };
    let r: (Option[S], G) = step(g, 5);
    let a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id + s.v.len(); }, None => { a = a + 100; } }
    let r2: (Option[S], G) = step(r.1, 0);
    match (r2.0) { Some(s2) => { a = a + 100; }, None => { a = a + r2.1.i; } }
    return a;
}`},
	{"some_struct_ident_payload", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function f(g: G): (Option[S], G) {
    let p: S = S { id: 5, v: "hit" };
    return (Some(p), G { ...g, i: 9 });
}
function main(): i32 {
    let r: (Option[S], G) = f(G { i: 0, name: "n" });
    let a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 100; } }
    return a;
}`},
	{"some_struct_scalar_pair", `struct S { id: i32, v: string }
function f(): (Option[S], i32) { return (Some(S { id: 5, v: "hit" }), 4); }
function main(): i32 {
    let r: (Option[S], i32) = f();
    let a: i32 = r.1;
    match (r.0) { Some(s) => { a = a + s.id + s.v.len(); }, None => { a = a + 100; } }
    return a;
}`},
	{"fn_call_option_struct_control", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function pick(k: i32): Option[S] {
    if (k > 0) { return Some(S { id: k, v: "hit" }); }
    return None;
}
function f(g: G): (Option[S], G) { return (pick(5), G { ...g, i: 9 }); }
function main(): i32 {
    let r: (Option[S], G) = f(G { i: 0, name: "n" });
    let a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 100; } }
    return a;
}`},
	{"option_struct_local_control", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function f(g: G): (Option[S], G) {
    let o: Option[S] = Some(S { id: 5, v: "hit" });
    return (o, G { ...g, i: 9 });
}
function main(): i32 {
    let r: (Option[S], G) = f(G { i: 0, name: "n" });
    let a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 100; } }
    return a;
}`},
	{"none_control", `struct S { id: i32, v: string }
struct G { i: i32, name: string }
function f(g: G): (Option[S], G) { return (None, G { ...g, i: 9 }); }
function main(): i32 {
    let r: (Option[S], G) = f(G { i: 0, name: "n" });
    let a: i32 = r.1.i;
    match (r.0) { Some(s) => { a = a + s.id; }, None => { a = a + 3; } }
    return a;
}`},
	{"some_scalar_control", `struct G { i: i32, name: string }
function f(g: G): (Option[i32], G) { return (Some(7), G { ...g, i: 9 }); }
function main(): i32 {
    let r: (Option[i32], G) = f(G { i: 0, name: "n" });
    let a: i32 = r.1.i;
    match (r.0) { Some(x) => { a = a + x; }, None => { a = a + 100; } }
    return a;
}`},
}

func TestSelfHostTupleOptStructElemIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleOptStructElemCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostTupleOptStructElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleOptStructElemCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostTupleOptStructElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple Option[struct] element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupleOptStructElemCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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

const tupleOptStructStampedSrc = `struct S { id: i32, v: string }
struct G { i: i32, name: string }
struct Tab { n: i32 }
function (t: Tab) find(k: i32): Option[S] {
    if (k > 0) { return Some(S { id: k + t.n, v: "hit" }); }
    return None;
}
function f(g: G, t: Tab): (Option[S], G) { return (t.find(5), G { ...g, i: 9 }); }
function main(): i32 {
    let r: (Option[S], G) = f(G { i: 0, name: "n" }, Tab { n: 2 });
    let a: i32 = r.1.i;
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

const tupleOptEnumStampedSrc = `enum E { A, B(i32) }
struct C { n: i32 }
function step(c: C, k: i32): (C, Option[E]) {
    let e: E = B(k);
    if (k > 0) { return (C { n: c.n + k }, Some(e)); }
    return (C { n: c.n + 1 }, None);
}
function main(): i32 {
    let r: (C, Option[E]) = step(C { n: 0 }, 5);
    let a: i32 = r.0.n;
    match (r.1) { Some(x) => { match (x) { A => { a = a + 50; }, B(v) => { a = a + v; } } }, None => { a = a + 100; } }
    let r2: (C, Option[E]) = step(r.0, 0);
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
