package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Generic numeric reducers in std/num — `sum_with[T: Add]` / `product_with[T:
// Mul]` (#2706, the trait-spine epic #2691). One generic body folds an array via
// the `.add` / `.mul` trait method, working for every primitive width AND any
// user type that impls the trait. A bounded-generic method call monomorphises to
// a direct call, so these lower on the self-host IR path. All oracles < 256 so
// the i32 result survives the process exit code.
var numReducerCases = []struct {
	name string
	main string
	want int
}{
	// sum_with over i32: 10+20+5+7 starting from 0 = 42.
	{"sum-i32", `pub trait Add { function add(self: Self, o: Self): Self; }
impl Add for i32 { function add(self: Self, o: Self): Self { return self + o; } }
function sum_with[T: Add](xs: T[], zero: T): T { var acc = zero; for x in xs { acc = acc.add(x); } return acc; }
function main(): i32 { return sum_with([10, 20, 5, 7], 0); }`, 42},
	// product_with over i32: 1*2*3*4 starting from 1 = 24.
	{"product-i32", `pub trait Mul { function mul(self: Self, o: Self): Self; }
impl Mul for i32 { function mul(self: Self, o: Self): Self { return self * o; } }
function product_with[T: Mul](xs: T[], one: T): T { var acc = one; for x in xs { acc = acc.mul(x); } return acc; }
function main(): i32 { return product_with([1, 2, 3, 4], 1); }`, 24},
	// empty array returns the identity unchanged: sum_with([], 9) = 9.
	{"sum-empty", `pub trait Add { function add(self: Self, o: Self): Self; }
impl Add for i32 { function add(self: Self, o: Self): Self { return self + o; } }
function sum_with[T: Add](xs: T[], zero: T): T { var acc = zero; for x in xs { acc = acc.add(x); } return acc; }
function main(): i32 { var e: i32[] = []; return sum_with(e, 9); }`, 9},
	// the SAME generic sum over a user Add struct (2D vector): (1,1)+(2,3)+(3,5) =
	// (6,9); 6*10 + 9 = 69.
	{"sum-user-vector", `pub trait Add { function add(self: Self, o: Self): Self; }
struct V2 { x: i32, y: i32 }
impl Add for V2 { function add(self: Self, o: Self): Self { return V2 { x: self.x + o.x, y: self.y + o.y }; } }
function sum_with[T: Add](xs: T[], zero: T): T { var acc = zero; for x in xs { acc = acc.add(x); } return acc; }
function main(): i32 { var vs: V2[] = [V2 { x: 1, y: 1 }, V2 { x: 2, y: 3 }, V2 { x: 3, y: 5 }]; var t = sum_with(vs, V2 { x: 0, y: 0 }); return t.x * 10 + t.y; }`, 69},
	// the SAME generic sum at a different width (i64): 30+11 = 41.
	{"sum-i64", `pub trait Add { function add(self: Self, o: Self): Self; }
impl Add for i64 { function add(self: Self, o: Self): Self { return self + o; } }
function sum_with[T: Add](xs: T[], zero: T): T { var acc = zero; for x in xs { acc = acc.add(x); } return acc; }
function main(): i32 { var xs: i64[] = [30, 11]; var s = sum_with(xs, 0); return s as i32; }`, 41},
}

// TestNativeNumReducers runs the inline reducer programs on the native interp /
// x86-64 / wasm backends, oracle-checked.
func TestNativeNumReducers(t *testing.T) {
	for _, tc := range numReducerCases {
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

// TestNativeNumReducersArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeNumReducersArm64(t *testing.T) {
	for _, tc := range numReducerCases {
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

// TestNativeNumReducersModule exercises the shipped `import "std/num"` module's
// sum_with / product_with over primitives AND a user Add struct.
func TestNativeNumReducersModule(t *testing.T) {
	src := `import "std/num" as num;
struct V2 { x: i32, y: i32 }
impl num.Add for V2 { function add(self: Self, o: Self): Self { return V2 { x: self.x + o.x, y: self.y + o.y }; } }
function main(): i32 {
    var s = num.sum_with([10, 20, 5, 7], 0);                 // 42
    var p = num.product_with([1, 2, 3, 4], 1);               // 24
    var vs: V2[] = [V2 { x: 1, y: 1 }, V2 { x: 2, y: 3 }];
    var v = num.sum_with(vs, V2 { x: 0, y: 0 });             // (3,4)
    return s + p + v.x * 10 + v.y;                           // 42+24+30+4 = 100
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 100 {
		t.Errorf("num reducers module interp = %d, want 100", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 100 {
		t.Errorf("num reducers module x86-64 = %d, want 100", code)
	}
	if code := runWasm(t, src); code != 100 {
		t.Errorf("num reducers module wasm = %d, want 100", code)
	}
}

// TestSelfHostNumReducersIRX86_64 routes each inline case through the self-hosted
// x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostNumReducersIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range numReducerCases {
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

// TestSelfHostNumReducersIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostNumReducersIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host num-reducers wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range numReducerCases {
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
			watFile := filepath.Join(dir, "num_reducers_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("num reducers wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
