package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Associated-function calls on a BOUND type parameter — `T.zero()` / `T.one()`
// where `T: Zero` / `T: One` (std/num's additive / multiplicative identities) —
// on the self-host IR path. The primitive Zero/One impls are not compiled in
// the IR path (same as `Default`), so before this fix `T.zero()` monomorphised
// to an unresolved `i32.zero()` and bailed the whole module to the legacy AST
// emitter. The monomorphiser now emits the identity literal directly (i32→0/1,
// f64→0.0/1.0), mirroring the existing `T.default()` scalar-literal lowering,
// so a generic numeric reducer `sum[T: Add + Zero](xs): T { var acc = T.zero();
// … }` lowers through the IR path on every backend. Oracle-checked against the
// native backend; routing pinned to "ir".
var assocIdentityCases = []struct {
	name string
	main string
	want int
}{
	// T.zero() on a bound param → the additive identity literal. z_of(7)=0, +42.
	{"zero-i32", `pub trait Zero { function zero(): Self; }
impl Zero for i32 { function zero(): Self { return 0; } }
function z_of[T: Zero](x: T): T { return T.zero(); }
function main(): i32 { return z_of(7) + 42; }`, 42},
	// T.one() on a bound param → the multiplicative identity literal. o_of(7)=1, +41.
	{"one-i32", `pub trait One { function one(): Self; }
impl One for i32 { function one(): Self { return 1; } }
function o_of[T: One](x: T): T { return T.one(); }
function main(): i32 { return o_of(7) + 41; }`, 42},
	// the payoff: a generic numeric reducer seeded from T.zero() and folding with
	// the bound's .add — 10+20+12 = 42.
	{"sum-add-zero", `pub trait Add { function add(self: Self, o: Self): Self; }
pub trait Zero { function zero(): Self; }
impl Add for i32 { function add(self: Self, o: Self): Self { return self + o; } }
impl Zero for i32 { function zero(): Self { return 0; } }
function sum[T: Add + Zero](xs: T[]): T { var acc: T = T.zero(); var i = 0; while (i < xs.len()) { acc = acc.add(xs[i]); i = i + 1; } return acc; }
function main(): i32 { var xs: i32[] = [10, 20, 12]; return sum(xs); }`, 42},
	// product seeded from T.one(), folding with .mul — 2*3*7 = 42.
	{"product-mul-one", `pub trait Mul { function mul(self: Self, o: Self): Self; }
pub trait One { function one(): Self; }
impl Mul for i32 { function mul(self: Self, o: Self): Self { return self * o; } }
impl One for i32 { function one(): Self { return 1; } }
function product[T: Mul + One](xs: T[]): T { var acc: T = T.one(); var i = 0; while (i < xs.len()) { acc = acc.mul(xs[i]); i = i + 1; } return acc; }
function main(): i32 { var xs: i32[] = [2, 3, 7]; return product(xs); }`, 42},
}

// TestNativeAssocIdentity runs the associated-identity programs through the
// native interpreter + x86-64 + wasm backends, oracle-checked.
func TestNativeAssocIdentity(t *testing.T) {
	for _, tc := range assocIdentityCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.main+"\n"); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostAssocIdentityIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, pins routing to "ir" (the monomorphiser now lowers
// `T.zero()` / `T.one()` to a literal instead of bailing), and oracle-checks
// the exit code.
func TestSelfHostAssocIdentityIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range assocIdentityCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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

// TestSelfHostAssocIdentityIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostAssocIdentityIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host assoc-identity wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range assocIdentityCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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
			watFile := filepath.Join(dir, "assoc_identity_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("assoc-identity wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
