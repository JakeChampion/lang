package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var rangeBoundOrderCases = []struct {
	name string
	body string
	want string
}{
	{"exclusive", `for i in mark("low", 2)..mark("high", 4) { print(i); }`, "low\nhigh\n2\n3\n"},
	{"inclusive", `for i in mark("low", 2)..=mark("high", 4) { print(i); }`, "low\nhigh\n2\n3\n4\n"},
	{"exclusive-empty", `for i in mark("low", 4)..mark("high", 2) { print(i); }`, "low\nhigh\n"},
	{"inclusive-empty", `for i in mark("low", 4)..=mark("high", 2) { print(i); }`, "low\nhigh\n"},
	{"exclusive-shadow", `let i = 4; for i in mark("low", 2)..mark("high", i) { print(i); } print(i);`, "low\nhigh\n2\n3\n4\n"},
	{"inclusive-shadow", `let i = 4; for i in mark("low", 2)..=mark("high", i) { print(i); } print(i);`, "low\nhigh\n2\n3\n4\n4\n"},
	{"exclusive-continue", `for i in mark("low", 2)..mark("high", 4) { print(i); continue; }`, "low\nhigh\n2\n3\n"},
	{"inclusive-max-continue", `for i in mark("low", 2147483647)..=mark("high", 2147483647) { print(i); continue; }`, "low\nhigh\n2147483647\n"},
	{"inclusive-break", `for i in mark("low", 2)..=mark("high", 4) { print(i); break; }`, "low\nhigh\n2\n"},
	{"nested", `for i in mark("outer-low", 0)..mark("outer-high", 2) { for j in mark("inner-low", 0)..mark("inner-high", 1) { print(i); print(j); } }`, "outer-low\nouter-high\ninner-low\ninner-high\n0\n0\ninner-low\ninner-high\n1\n0\n"},
	{"body-shadow", `for i in mark("low", 2)..mark("high", 4) { let i = 9; print(i); }`, "low\nhigh\n9\n9\n"},
	{"deferred-continue", `for i in mark("low", 2)..mark("high", 4) { defer print(i); continue; }`, "low\nhigh\n2\n3\n"},
	{"captured-turns", `let fs: (() => i32)[] = []; for i in mark("low", 0)..mark("high", 2) { fs = fs.append((): i32 => i); } print(fs[0]()); print(fs[1]());`, "low\nhigh\n0\n1\n"},
}

func TestRangeBoundsEvaluationOrder(t *testing.T) {
	for _, tc := range rangeBoundOrderCases {
		t.Run(tc.name, func(t *testing.T) {
			source := `function mark(label: string, n: i32): i32 { print(label); return n; }
function main(): i32 { ` + tc.body + ` return 0; }
`
			for _, target := range []string{"go", "fern-interp", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					var out string
					var code int
					switch target {
					case "go":
						out, code = runInterpExitCode(t, source)
					case "fern-interp":
						entry := filepath.Join(t.TempDir(), "main.fern")
						if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
							t.Fatal(err)
						}
						cmd := exec.Command(e2eharness.SelfHostCLI(t), "-interp", entry, e2eharness.SelfHostStdlibRoot(t))
						data, err := cmd.CombinedOutput()
						if cmd.ProcessState == nil {
							t.Fatalf("interpreter did not start: %v", err)
						}
						out, code = string(data), cmd.ProcessState.ExitCode()
					case "x86-64-linux":
						out, code = compileAndRunX86_64(t, source)
					case "arm64-linux":
						out, code = compileAndRunArm64(t, source)
					default:
						stdout, stderr, exit := runWasmArtifact(t, buildCLIComponent(t, source), runOpts{})
						out, code = stdout+stderr, exit
					}
					if code != 0 || out != tc.want {
						t.Fatalf("exit %d, output %q, want %q", code, out, tc.want)
					}
				})
			}
		})
	}
}
