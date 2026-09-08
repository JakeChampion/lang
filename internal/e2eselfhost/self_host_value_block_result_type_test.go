package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Use raw IR drivers deliberately: loader annotation already supplies these
// result types. The binding must also preserve the lowerer's typed result slot
// after lexical retirement, without relying on leaked pattern-binding names.
func TestSelfHostValueBlockResultTypesIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	cases := []struct {
		name, source string
		want         int
	}{
		{"option-string", `function main(): i32 { var o: Option[string] = Some("hi"); var s = match (o) { Some(v) => v, None => "" }; return s.len(); }`, 2},
		{"option-none", `function main(): i32 { var o: Option[string] = None; var s = match (o) { Some(v) => v, None => "none" }; return s.len(); }`, 4},
		{"result-string", `function main(): i32 { var r: Result[string, i32] = Ok("abcd"); var s = match (r) { Ok(v) => v, Err(e) => "" }; return s.len(); }`, 4},
		{"result-error", `function main(): i32 { var r: Result[string, i32] = Err(1); var s = match (r) { Ok(v) => v, Err(e) => "error" }; return s.len(); }`, 5},
		{"shadowed-payload", `function main(): i32 { var v = "outer"; var o: Option[string] = Some("hi"); var s = match (o) { Some(v) => v, None => "" }; return s.len() * 10 + v.len(); }`, 25},
		{"sibling-payloads", `function main(): i32 { var a: Option[string] = Some("hi"); var b: Option[string] = Some("abcd"); var s = match (a) { Some(v) => v, None => "" }; var t = match (b) { Some(v) => v, None => "" }; return (s + t).len(); }`, 6},
		{"numeric-control", `function main(): i32 { var o: Option[i32] = Some(7); var n = match (o) { Some(v) => v, None => 0 }; return n + 1; }`, 8},
	}
	for _, target := range []string{"x86-64", "arm64", "wasm"} {
		t.Run(target, func(t *testing.T) {
			var armgcc, armRunner string
			if target == "arm64" {
				armgcc, armRunner = arm64Tooling(t)
			}
			if target == "wasm" {
				if _, err := exec.LookPath("wasmtime"); err != nil {
					t.Skip("wasmtime not on PATH")
				}
			}
			dir := t.TempDir()
			driverFile := "asm_ir_run.fern"
			if target == "wasm" {
				driverFile = "wasm_ir_run.fern"
			}
			copySelfHostDriver(t, dir, driverFile)
			driver := buildSelfHostBin(t, gcc, dir, driverFile, "driver")
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if want := interpExit(t, interpBin, tc.source); want != tc.want {
						t.Fatalf("interpreter exit %d, want %d", want, tc.want)
					}
					args := []string{"-ir"}
					if target == "arm64" {
						args = append(args, "-target", "arm64-linux")
					}
					cmd := runX86_64Bin(runner, driver, args...)
					cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
					cmd.Stdin = strings.NewReader(tc.source)
					output, err := cmd.Output()
					if err != nil {
						t.Fatalf("strict IR: %v: %s", err, exitStderr(err))
					}
					var run *exec.Cmd
					switch target {
					case "x86-64":
						bin := buildBin(t, gcc, dir, tc.name, string(output))
						run = runX86_64Bin(runner, bin)
					case "arm64":
						bin := buildBinArm64(t, armgcc, dir, tc.name, string(output))
						run = runArm64Bin(armRunner, bin)
					case "wasm":
						path := filepath.Join(t.TempDir(), "main.wat")
						if err := os.WriteFile(path, output, 0o644); err != nil {
							t.Fatal(err)
						}
						run = exec.Command("wasmtime", "run", path)
					}
					got, _ := run.CombinedOutput()
					if run.ProcessState == nil {
						t.Fatalf("program did not start: %s", got)
					}
					if code := run.ProcessState.ExitCode(); code != tc.want {
						t.Errorf("exit %d, want %d: %s", code, tc.want, got)
					}
				})
			}
		})
	}
}
