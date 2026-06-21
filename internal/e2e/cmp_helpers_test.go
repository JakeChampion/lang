package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Generic Ord helpers in core/cmp — `min` / `max` / `clamp` / `lt` … over any
// `T: Ord`, derived from the single `cmp` primitive. A bounded generic whose
// body calls a trait method on the bound parameter monomorphises to a direct
// call, so these lower on the native backends AND the self-host IR path
// (#2691 / #3558). The inline cases below pin the self-host IR routing; a
// native test exercises the shipped module (incl. the primitive `impl Ord for
// i32` and a user `Ord` struct).
var cmpHelperCases = []struct {
	name string
	main string
	want int
}{
	// min/max over a user Ord struct (P ordered by .v). min(5,2).v + max(5,2).v = 2+5 = 7.
	{"min-max", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function min[T: Ord](a: T, b: T): T { if (b.cmp(a) < 0) { return b; } return a; }
function max[T: Ord](a: T, b: T): T { if (a.cmp(b) < 0) { return b; } return a; }
function main(): i32 { var lo = min(P { v: 5 }, P { v: 2 }); var hi = max(P { v: 5 }, P { v: 2 }); return lo.v + hi.v; }`, 7},
	// clamp(9, 1, 6).v = 6.
	{"clamp", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function clamp[T: Ord](x: T, lo: T, hi: T): T { if (x.cmp(lo) < 0) { return lo; } if (x.cmp(hi) > 0) { return hi; } return x; }
function main(): i32 { var c = clamp(P { v: 9 }, P { v: 1 }, P { v: 6 }); return c.v; }`, 6},
	// relational helpers return boolean (direct bounded-generic). lt(2,9)=true → 5.
	{"lt-gte", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function lt[T: Ord](a: T, b: T): boolean { return a.cmp(b) < 0; }
function gte[T: Ord](a: T, b: T): boolean { return a.cmp(b) >= 0; }
function main(): i32 { var r = 0; if (lt(P { v: 2 }, P { v: 9 })) { r = r + 5; } if (gte(P { v: 9 }, P { v: 9 })) { r = r + 2; } return r; }`, 7},
}

// TestNativeCmpHelpers runs the inline Ord-helper programs on the native
// interp / x86-64 / wasm backends, oracle-checked.
func TestNativeCmpHelpers(t *testing.T) {
	for _, tc := range cmpHelperCases {
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

// TestNativeCmpHelpersArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeCmpHelpersArm64(t *testing.T) {
	for _, tc := range cmpHelperCases {
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

// TestNativeCmpModule exercises the shipped `import "core/cmp"` module: the
// generic helpers over the primitive `impl Ord for i32` AND a user Ord struct.
func TestNativeCmpModule(t *testing.T) {
	src := `import "core/cmp" as cmp;
struct P { v: i32 }
impl cmp.Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function main(): i32 {
    var a = cmp.min(8, 3);               // 3   (primitive impl Ord for i32)
    var b = cmp.max(8, 3);               // 8
    var c = cmp.clamp(15, 0, 10);        // 10
    var p = cmp.min(P { v: 5 }, P { v: 2 }); // P{v:2}
    var d = 0;
    if (cmp.lt(2, 9)) { d = 1; }         // 1
    return a + b + c + p.v + d;          // 3+8+10+2+1 = 24
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 24 {
		t.Errorf("cmp module interp = %d, want 24", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 24 {
		t.Errorf("cmp module x86-64 = %d, want 24", code)
	}
	if code := runWasm(t, src); code != 24 {
		t.Errorf("cmp module wasm = %d, want 24", code)
	}
}

// TestSelfHostCmpHelpersIRX86_64 routes each inline Ord-helper case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostCmpHelpersIRX86_64(t *testing.T) {
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

	for _, tc := range cmpHelperCases {
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

// TestSelfHostCmpHelpersIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostCmpHelpersIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host cmp-helper wasm IR e2e")
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

	for _, tc := range cmpHelperCases {
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
			watFile := filepath.Join(dir, "cmp_helper_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("cmp-helper wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
