package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostLambdaScopeRuntime(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	compiler := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	const captureModule = `pub struct Node { value: i32 }
function apply(node: Node, callback: (Node) => Node): Node { return callback(node); }
pub function run(callback: (Node) => Node): i32 {
    var node = apply(Node { value: 7 }, (n: Node): Node => callback(n));
    return node.value;
}`
	writeProject := func(src string) string {
		t.Helper()
		proj := t.TempDir()
		for name, text := range map[string]string{"main.fern": src, "capture_api.fern": captureModule} {
			if err := os.WriteFile(filepath.Join(proj, name), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(proj, "main.fern")
	}
	cases := []struct{ name, src string }{
		{"initializer-reads-outer", `function main(): i32 {
var n: i32 = 6; var call = (): i32 => { var n = n + 1; return n; }; return call(); }`},
		{"read-before-local", `function main(): i32 {
var n: i32 = 7; var call = (): i32 => { var answer = n; var n = 99; return answer; }; return call(); }`},
		{"branch-binder-does-not-escape", `function main(): i32 {
var n: i32 = 7; var call = (): i32 => { if (false) { var n = 99; } return n; }; return call(); }`},
		{"loop-binder-does-not-escape", `function main(): i32 {
var n: i32 = 7; var call = (): i32 => { for n in [99] { } return n; }; return call(); }`},
		{"nested-closure-before-local", `function main(): i32 {
var n: i32 = 7; var call = (): i32 => { var inner = (): i32 => n; var n = 99; return inner(); }; return call(); }`},
		{"wide-initializer-reads-outer", `function main(): i32 {
var n: i64 = 5000000000; var call = (): i64 => { var n = n + 7; return n; };
if (call() == 5000000007) { return 7; } return 99; }`},
		{"mutable-capture-before-shadow", `function main(): i32 {
var n: i32 = 1; var call = (): i32 => { n = 7; var inner = (): i32 => n; var n = 99; return inner(); };
var result = call(); if (n != 7) { return 98; } return result; }`},
		{"tuple-capture-before-shadow", `function main(): i32 {
var (n, other) = (7, 8); var call = (): i32 => { var inner = (): i32 => n; var (n, other) = (99, 98); return inner(); };
return call(); }`},
		{"guarded-pattern-capture", `enum E { Full(i32), Empty }
function main(): i32 {
var answer: i32 = 0;
match (E.Full(7)) { Full(n) when n == 7 => { var call = (): i32 => n; var n = 99; answer = call(); }, _ => { return 98; } }
return answer; }`},
		{"imported-callback-capture", `import "./capture_api";
function main(): i32 { return capture_api.run((n: capture_api.Node): capture_api.Node => n); }`},
	}
	for _, tc := range cases {
		if _, got := runFixtureInterp(t, writeProject(tc.src), ""); got != 7 {
			t.Fatalf("%s: interpreter = %d, want independently pinned 7", tc.name, got)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					main := writeProject(tc.src)
					proj := filepath.Dir(main)
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
