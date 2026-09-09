package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostFloatWidthRuntime(t *testing.T) {
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
		{"result-widths", `function main(): i32 { var a: f32 = 16777216.0; var b: f64 = 16777216.0;
var r: Result[f32, f64] = Ok(a); var e: Result[f32, f64] = Err(b);
var x = match (r) { Ok(v) => v, Err(_) => 0.5 };
var y = match (e) { Ok(_) => 0.5, Err(v) => v };
if (x + 1.0 == a && y + 1.0 > b) { return 7; } return 99; }`},
		{"call-receiver", `function small(a: f32): f32 { return a; }
function (a: f32) kind(): i32 { return 7; }
function (a: f64) kind(): i32 { return 99; }
function main(): i32 { return small(1.0).kind(); }`},
		{"call-projection", `function pair(a: f32, b: f64): (f32, f64) { return (a, b); }
function main(): i32 { var a: f32 = 16777216.0; var b: f64 = 16777216.0;
if (pair(a, b).0 + 1.0 == a && pair(a, b).1 + 1.0 > b) { return 7; } return 99; }`},
		{"array-projection", `function main(): i32 { var a: f32 = 16777216.0; var b: f64 = 16777216.0;
var xs: f32[] = [a]; var ys: f64[] = [b];
if (xs[0] + 1.0 == a && ys[0] + 1.0 > b) { return 7; } return 99; }`},
		{"option-join", `function main(): i32 { var a: f32 = 16777216.0; var o: Option[f32] = Some(a);
var v = match (o) { Some(x) => x, None => 0.5 };
if (v + 1.0 == a) { return 7; } return 99; }`},
		{"block-join", `function main(): i32 { var a: f32 = 16777216.0;
var v = if (a > 0.0) { var x = a; x } else { var y = a; y };
if (v + 1.0 == a) { return 7; } return 99; }`},
		{"call-width", `function small(a: f32): f32 { return a; }
function large(a: f64): f64 { return a; }
function main(): i32 { var a: f32 = 16777216.0; var b: f64 = 16777216.0;
if (small(a) + 1.0 == a && large(b) + 1.0 > b) { return 7; } return 99; }`},
		{"receiver-width", `function (a: f32) next(): f32 { return a + 1.0; }
function (a: f64) next(): f64 { return a + 1.0; }
function main(): i32 { var a: f32 = 16777216.0; var b: f64 = 16777216.0;
if (a.next() == a && b.next() > b) { return 7; } return 99; }`},
		{"if-join", `function pick(c: boolean, a: f32): f32 { return if (c) { 0.5 } else { a }; }
function main(): i32 { var a: f32 = 16777216.0;
if (pick(false, a) + 1.0 != a) { return 91; }
if (pick(true, a) != 0.5) { return 92; } return 7; }`},
		{"tuple-match-join", `function pick(c: i32, a: f32): (f32, i32) { return match(c) { 0 => (0.5, 1), _ => (a, 2) }; }
function main(): i32 { var a: f32 = 16777216.0; var v = pick(1, a);
if (v.0 + 1.0 == a && v.1 == 2) { return 7; } return 99; }`},
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
