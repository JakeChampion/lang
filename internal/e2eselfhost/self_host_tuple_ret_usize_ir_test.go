package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleRetUsizeCases pin `usize` as a lowerable TUPLE-ELEMENT and struct-FIELD
// type on the self-host IR path. Both eligibility gates — tuple_elems_lowerable
// and decl_is_leaksafe_at_d — listed every other scalar spelling but not
// `usize`, so a `(usize, usize)`-returning function, or a struct with a `usize`
// field, bailed the whole module (`coreutils/lib/base.fern`'s `tables()` /
// `Codec`, which is what made base32 / base64 / basenc fail their self-host leg).
//
// A `usize` is a raw address, so it wants the WIDTH-0 tuple slot every other
// pointer element already uses: the register backends move 8 bytes for every
// element kind and wasm32's pointer IS the i32 that width-0 stores and loads.
// The cases therefore round-trip real `__alloc` addresses through a tuple and a
// struct and read memory back through them — a truncated or mis-widened element
// misses the block it wrote.
var tupleRetUsizeCases = []struct {
	name string
	src  string
	exit int
}{
	// The bail shape itself: return two allocations as a `(usize, usize)`, read
	// each back through `.N`.
	{"usize-tuple-ret-dot", `function mk(): (usize, usize) { var a: usize = __alloc(64); var b: usize = __alloc(64); __store_i32(a, 11); __store_i32(b, 22); return (a, b); }
function main(): i32 { var t = mk(); return __load_i32(t.0) + __load_i32(t.1); }`, 33},
	// Same return, bound through the DECLARED annotation `base.fern` writes.
	{"usize-tuple-ret-annotated", `function mk(): (usize, usize) { var a: usize = __alloc(64); __store_i32(a, 6); var b: usize = __alloc(64); __store_i32(b, 7); return (a, b); }
function main(): i32 { var t: (usize, usize) = mk(); return __load_i32(t.0) + __load_i32(t.1); }`, 13},
	// Destructured, so the binds take the tuple-tag path rather than `.N`.
	{"usize-tuple-ret-destructure", `function mk(): (usize, usize) { var a: usize = __alloc(64); var b: usize = __alloc(64); __store_i32(a, 4); __store_i32(b, 5); return (a, b); }
function main(): i32 { var (p, q) = mk(); return __load_i32(p) * __load_i32(q); }`, 20},
	// Mixed widths in one box: a pointer slot beside an i32 slot.
	{"usize-tuple-ret-mixed", `function mk(n: i32): (usize, i32) { var a: usize = __alloc(64); __store_i32(a, n); return (a, n + 1); }
function main(): i32 { var t = mk(9); return __load_i32(t.0) + t.1; }`, 19},
	// Address ARITHMETIC off a tuple element — `base.fern`'s `d + (i * 4) as
	// usize` store loop. A truncated element writes outside its block, so the
	// read-back guard is what catches it.
	{"usize-tuple-elem-offset", `function mk(): (usize, usize) { var a: usize = __alloc(256); var b: usize = __alloc(256); return (a, b); }
function main(): i32 { var t = mk(); __store_i32(t.0 + 32 as usize, 41); __store_i32(t.1 + 64 as usize, 1); if (__load_i32(t.0 + 32 as usize) != 41) { return 90; } if (__load_i32(t.1 + 64 as usize) != 1) { return 91; } return 7; }`, 7},
	// The struct half of the same gap: `Codec`'s two `usize` fields made the
	// whole struct not leak-safe, which bailed its constructor.
	{"usize-struct-field", `struct C { p: usize, n: i32, q: usize }
function mk(): C { var a: usize = __alloc(64); __store_i32(a, 3); var b: usize = __alloc(64); __store_i32(b, 4); return C { p: a, n: 5, q: b }; }
function main(): i32 { var c = mk(); return __load_i32(c.p) + c.n + __load_i32(c.q); }`, 12},
	// `char` was absent from both lists for the same reason and bailed the same
	// way; it is a code point in the one i32 slot the receiver gate already
	// classes it as.
	{"char-tuple-ret", `function mk(): (char, i32) { var c: char = 'A'; return (c, 1); }
function main(): i32 { var t = mk(); if (t.0 == 'A') { return 6; } return t.1; }`, 6},
	{"char-struct-field", `struct K { c: char, n: i32 }
function mk(): K { return K { c: 'z', n: 3 }; }
function main(): i32 { var k = mk(); if (k.c == 'z') { return k.n + 4; } return 0; }`, 7},
}

// TestSelfHostTupleRetUsizeIRX86_64 drives the cases through the self-hosted
// x86-64 backend, where a tuple element is an 8-byte slot and a `usize` is an
// 8-byte address.
func TestSelfHostTupleRetUsizeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range tupleRetUsizeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostTupleRetUsizeIRArm64 — the arm64 counterpart. The gates live in
// shared irlower.fern, so the case table is shared with the x86-64 leg.
func TestSelfHostTupleRetUsizeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleRetUsizeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostTupleRetUsizeIRWasm — the wasm32 leg, and the one that answers
// the pointer-width question the other way round: there a `usize` is 4 bytes
// and the width-0 tuple slot is exactly the i32.store / i32.load it needs.
func TestSelfHostTupleRetUsizeIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-ret-usize wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupleRetUsizeCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
