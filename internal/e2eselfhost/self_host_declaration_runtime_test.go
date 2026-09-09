package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostDeclarationRuntime(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	compiler := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	interp := buildLangBinForInterp(t)
	stdlib, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, src string }{
		{"grouped-wide", `function sum(a: i64, b: f64): i64 { return a + (b as i64); }
function main(): i32 { var call: ((i64, f64) => i64) = sum;
if (call(5000000000, 7.0) == 5000000007) { return 7; } return 99; }`},
		{"array-returning-callables", `function make(n: i32): i64[] { return [5000000000 + (n as i64)]; }
function main(): i32 { var call: ((i32) => i64[]) = make;
var fs: (((i32) => i64[]))[] = [call]; var xs = fs[0](7);
if (xs[0] == 5000000007) { return 7; } return 99; }`},
		{"nested-callable-result", `function plus(n: i64): i64 { return n + 7; }
function make(): ((i64) => i64) { return plus; }
function main(): i32 { var factory: (() => ((i64) => i64)) = make; var call = factory();
if (call(5000000000) == 5000000007) { return 7; } return 99; }`},
		{"mixed-float-widths", `function sum(a: f32, b: f64): f64 { return (a as f64) + b; }
function main(): i32 { var call: ((f32, f64) => f64) = sum;
if (call(3.5, 4.5) == 8.0) { return 7; } return 99; }`},
		{"branch-inferred-callable", `function plus(n: i64): i64 { return n + 7; }
function make(): ((i64) => i64) { return plus; }
function apply(flag: boolean): i32 { if (flag) { var call = make();
if (call(5000000000) == 5000000007) { return 7; } } return 99; }
function main(): i32 { return apply(true); }`},
		{"loop-inferred-callable", `function plus(n: i64): i64 { return n + 7; }
function make(): ((i64) => i64) { return plus; }
function main(): i32 { for i in [0, 1] { var call = make();
if (call(5000000000) != 5000000007) { return 99; } } return 7; }`},
		{"capture-inferred-callable", `function plus(n: i64): i64 { return n + 7; }
function make(): ((i64) => i64) { return plus; }
function main(): i32 { var call = make(); var thunk = (): i64 => call(5000000000);
if (thunk() == 5000000007) { return 7; } return 99; }`},
		{"returned-callable-argument", `function plus(n: i64): i64 { return n + 7; }
function make(): ((i64) => i64) { return plus; }
function apply(f: (i64) => i64): i64 { return f(5000000000); }
function main(): i32 { if (apply(make()) == 5000000007) { return 7; } return 99; }`},
		{"returned-callable-join", `function plus(n: i64): i64 { return n + 7; }
function make(flag: boolean): ((i64) => i64) {
if (flag) { return plus; } var offset: i64 = 7; return (n: i64): i64 => n + offset; }
function main(): i32 { for flag in [false, true] { var call = make(flag);
if (call(5000000000) != 5000000007) { return 99; } } return 7; }`},
		{"returned-callable-shadow", `function plus(n: i64): i64 { return n + 99; }
function make(plus: (i64) => i64): ((i64) => i64) { return plus; }
function main(): i32 { var call = make((n: i64): i64 => n + 7);
if (call(5000000000) == 5000000007) { return 7; } return 99; }`},
		{"capture-array-result", `function values(n: i32): i64[] { return [5000000000 + (n as i64)]; }
function make(): ((i32) => i64[]) { return values; }
function main(): i32 { var call = make(); var thunk = (): i64[] => call(7); var xs = thunk();
if (xs[0] == 5000000007) { return 7; } return 99; }`},
	}
	for _, tc := range cases {
		if got := interpExit(t, interp, tc.src); got != 7 {
			t.Fatalf("%s: interpreter = %d, want independently pinned 7", tc.name, got)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					proj := t.TempDir()
					main := filepath.Join(proj, "main.fern")
					if err := os.WriteFile(main, []byte(tc.src), 0o644); err != nil {
						t.Fatal(err)
					}
					out := filepath.Join(proj, "out.asm")
					cmd := runX86_64Bin(runner, compiler, "-target", target, "-emit", "asm", main, stdlib, "-o", out)
					cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, output)
					}
					asm, err := os.ReadFile(out)
					if err != nil || len(asm) == 0 {
						t.Fatalf("read nonempty assembly: %v", err)
					}
					var run *exec.Cmd
					switch target {
					case "x86-64-linux":
						run = runX86_64Bin(runner, buildBin(t, gcc, proj, "out", string(asm)))
					case "arm64-linux":
						armgcc, armrunner := arm64Tooling(t)
						run = runArm64Bin(armrunner, buildBinArm64(t, armgcc, proj, "out", string(asm)))
					case "wasm32-wasi":
						if _, err := exec.LookPath("wasmtime"); err != nil {
							t.Skip("wasmtime not installed")
						}
						run = exec.Command("wasmtime", "run", out)
					}
					output, err := run.CombinedOutput()
					if run.ProcessState == nil || !run.ProcessState.Exited() || run.ProcessState.ExitCode() != 7 {
						t.Fatalf("runtime: %v, want exit 7\n%s", err, output)
					}
				})
			}
		})
	}
}
