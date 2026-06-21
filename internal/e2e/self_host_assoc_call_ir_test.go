package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Associated-function calls (receiver-less impl methods — `T.zero()`, `P.def()`)
// reached through a GENERIC bound on the self-host IR path. A direct call on a
// declared struct already lowered; the gap was a target that monomorphises to a
// PRIMITIVE type (`sum[T: Add + Zero]` at T=i32 calls `T.zero()` → `i32.zero()`),
// which the associated-call branch rejected (it accepted only struct / struct-
// returning targets), forcing the whole module onto the legacy AST emitter (which
// miscompiles it). irlower's branch now also accepts a `decl_is_prim_recv`
// target. This is the enabler for the zero-less generic numeric reducer
// `sum[T: Num + Zero](xs): T` — #2706's headline shape — at i32. Native handles
// every case; this pins the ones that lower through IR. All oracles < 256.
//
// Scope note: a DIRECT literal `i32.zero()` (parses to a different node) and an
// arg-less generic `mk[T: Zero]()` (T can't be inferred in monomorph) and the
// wider i64/f64 instantiations are orthogonal gaps still on the AST path; they
// are covered by TestNativeAssocCalls (correctness) but not the IR-routing test.
var assocCallNativeCases = []struct {
	name string
	src  string
	want int
}{
	// generic numeric sum seeded by T.zero() at i32: 10+20+5+7 = 42.
	{"generic-sum-zero-i32", `pub trait Add { function add(self: Self, o: Self): Self; }
pub trait Zero { function zero(): Self; }
impl Add for i32 { function add(self: Self, o: Self): Self { return self + o; } }
impl Zero for i32 { function zero(): Self { return 0; } }
function sum[T: Add + Zero](xs: T[]): T { var acc = T.zero(); for x in xs { acc = acc.add(x); } return acc; }
function main(): i32 { return sum([10, 20, 5, 7]); }`, 42},
	// the same at a user struct accumulator: 10+20+5 = 35.
	{"generic-sum-zero-struct", `pub trait Add { function add(self: Self, o: Self): Self; }
pub trait Zero { function zero(): Self; }
struct Acc { n: i32 }
impl Add for Acc { function add(self: Self, o: Self): Self { return Acc { n: self.n + o.n }; } }
impl Zero for Acc { function zero(): Self { return Acc { n: 0 }; } }
function sum[T: Add + Zero](xs: T[]): T { var acc = T.zero(); for x in xs { acc = acc.add(x); } return acc; }
function main(): i32 { var xs: Acc[] = [Acc { n: 10 }, Acc { n: 20 }, Acc { n: 5 }]; var t = sum(xs); return t.n; }`, 35},
	// direct struct associated call (already worked; regression guard): (1,2) -> 12.
	{"struct-direct", `pub trait Def { function def(): Self; }
struct P { x: i32, y: i32 }
impl Def for P { function def(): Self { return P { x: 1, y: 2 }; } }
function main(): i32 { var p = P.def(); return p.x * 10 + p.y; }`, 12},
	// direct struct associated call with args (regression guard): 5 -> 50.
	{"struct-args", `pub trait FromI { function from_i(n: i32): Self; }
struct W { x: i32 }
impl FromI for W { function from_i(n: i32): Self { return W { x: n }; } }
function main(): i32 { var w = W.from_i(5); return w.x * 10; }`, 50},
	// direct literal primitive associated call — correct natively (orthogonal AST
	// gap on self-host, so NOT in the IR-routing list below): 7.
	{"prim-direct", `pub trait Zero { function zero(): Self; }
impl Zero for i32 { function zero(): Self { return 7; } }
function main(): i32 { return i32.zero(); }`, 7},
}

// assocCallIRCases is the subset that lowers through the self-host IR path (the
// first four entries; the trailing prim-direct case stays on AST).
var assocCallIRCases = assocCallNativeCases[:4]

// TestNativeAssocCalls oracle-checks the full matrix on the native backends.
func TestNativeAssocCalls(t *testing.T) {
	for _, tc := range assocCallNativeCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.src+"\n"); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeAssocCallsArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeAssocCallsArm64(t *testing.T) {
	for _, tc := range assocCallNativeCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostAssocCallsIRX86_64 routes the IR-eligible cases through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostAssocCallsIRX86_64(t *testing.T) {
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

	for _, tc := range assocCallIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
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

// TestSelfHostAssocCallsIRWasm runs the IR-eligible cases through the wasm IR backend.
func TestSelfHostAssocCallsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host assoc-call wasm IR e2e")
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

	for _, tc := range assocCallIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
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
			watFile := filepath.Join(dir, "assoc_call_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("assoc call wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
