package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Eq-bounded sequence verbs in core/cmp — `contains` / `index_of` / `distinct`
// over any `T: Eq`, derived from the single `eq` primitive (#2689). Like the
// `Ord` helpers, a bounded generic whose body calls `x.eq(y)` monomorphises to a
// direct call, so these lower on the native backends AND the self-host IR path
// (#2691 / #3558). The inline cases below pin the self-host IR routing over a
// user `Eq` struct (genuine genericity, not i32-special-casing); a native module
// test exercises the shipped module over the primitive `impl Eq for i32` /
// `string` and a user struct.
var eqVerbCases = []struct {
	name string
	main string
	want int
}{
	// contains over a user Eq struct (P compared by .v). hit → +5, miss → +2 → 7.
	{"contains", `pub trait Eq { function eq(self: Self, other: Self): boolean; }
struct P { v: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.v == other.v; } }
function contains[T: Eq](xs: T[], target: T): boolean { for x in xs { if (x.eq(target)) { return true; } } return false; }
function main(): i32 { var xs: P[] = [P { v: 1 }, P { v: 2 }, P { v: 3 }]; var r = 0; if (contains(xs, P { v: 2 })) { r = r + 5; } if (!contains(xs, P { v: 9 })) { r = r + 2; } return r; }`, 7},
	// index_of: first match position; miss → -1. found at 2 → 2*10 + (-1+1) = 20.
	{"index-of", `pub trait Eq { function eq(self: Self, other: Self): boolean; }
struct P { v: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.v == other.v; } }
function index_of[T: Eq](xs: T[], target: T): i32 { var i = 0; for x in xs { if (x.eq(target)) { return i; } i = i + 1; } return 0 - 1; }
function main(): i32 { var xs: P[] = [P { v: 10 }, P { v: 20 }, P { v: 30 }]; return index_of(xs, P { v: 30 }) * 10 + (index_of(xs, P { v: 99 }) + 1); }`, 20},
	// distinct dedups [1,2,1,3,2] → [1,2,3] (first occurrence kept, in order).
	// Verified directly via field access on the result: d.len()*100 + d[0].v*10 +
	// d[2].v. (An earlier revision avoided d[i].v on a generic `struct[]` return,
	// believing it hit a codegen limit — that was a process-exit-code (u8)
	// truncation artifact in the oracle, not a real limit: the value was 313,
	// which the exit code wraps to 57. Field access compiles and runs correctly;
	// the oracle just has to stay < 256, so this case checks len + d[0] + d[2].)
	// 3*10 + 1 + 3 = 34.
	{"distinct", `pub trait Eq { function eq(self: Self, other: Self): boolean; }
struct P { v: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.v == other.v; } }
function contains[T: Eq](xs: T[], target: T): boolean { for x in xs { if (x.eq(target)) { return true; } } return false; }
function distinct[T: Eq](xs: T[]): T[] { var out: T[] = []; for x in xs { if (!contains(out, x)) { out = out.append(x); } } return out; }
function main(): i32 { var xs: P[] = [P { v: 1 }, P { v: 2 }, P { v: 1 }, P { v: 3 }, P { v: 2 }]; var d = distinct(xs); return d.len() * 10 + d[0].v + d[2].v; }`, 34},
}

// TestNativeEqVerbs runs the inline Eq-verb programs on the native interp /
// x86-64 / wasm backends, oracle-checked.
func TestNativeEqVerbs(t *testing.T) {
	for _, tc := range eqVerbCases {
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

// TestNativeEqVerbsArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeEqVerbsArm64(t *testing.T) {
	for _, tc := range eqVerbCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeEqVerbsModule exercises the shipped `import "core/cmp"` module: the
// Eq verbs over the primitive `impl Eq for i32` / `string` AND a user Eq struct.
func TestNativeEqVerbsModule(t *testing.T) {
	src := `import "core/cmp" as cmp;
struct P { v: i32 }
impl cmp.Eq for P { function eq(self: Self, other: Self): boolean { return self.v == other.v; } }
function main(): i32 {
    var xs = [10, 20, 30, 20];
    var a = 0; if (cmp.contains(xs, 30)) { a = 1; }       // 1
    var b = 0; match (cmp.index_of(xs, 20)) { Some(v) => { b = v; }, None => { b = 0 - 1; } } // 1 (first)
    var d = cmp.distinct(xs);                              // [10,20,30] len 3
    var ss = ["a", "b", "a"];
    var sc = 0; if (cmp.contains(ss, "b")) { sc = 1; }     // 1
    var si = 0; match (cmp.index_of(ss, "a")) { Some(v) => { si = v; }, None => { si = 0 - 1; } } // 0
    var sd = cmp.distinct(ss).len();                       // 2
    var pc = 0; if (cmp.contains([P { v: 5 }, P { v: 6 }], P { v: 6 })) { pc = 1; }  // 1
    return a + b + d.len() + sc + si + sd + pc;            // 1+1+3+1+0+2+1 = 9
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 9 {
		t.Errorf("eq-verbs module interp = %d, want 9", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 9 {
		t.Errorf("eq-verbs module x86-64 = %d, want 9", code)
	}
	if code := runWasm(t, src); code != 9 {
		t.Errorf("eq-verbs module wasm = %d, want 9", code)
	}
}

// TestSelfHostEqVerbsIRX86_64 drives each inline Eq-verb case through the
// self-hosted x86-64 compiler and oracle-checks the exit code. The scalar-
// returning verbs (`contains` → boolean, `index_of` → i32) lower on the IR path;
// `distinct` returns a generic `struct[]`, which (over a struct element type)
// rides the AST fallback — both produce the right answer, so this asserts
// behaviour rather than the routing tag (which differs by case, as in the
// predicate-adapter gate). The native legs above pin cross-backend correctness.
func TestSelfHostEqVerbsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range eqVerbCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.main+"\n"))
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

// TestSelfHostEqVerbsIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostEqVerbsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host eq-verbs wasm IR e2e")
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

	for _, tc := range eqVerbCases {
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
			watFile := filepath.Join(dir, "eq_verbs_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("eq-verbs wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
