package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostPerModuleCaptureTypes(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_modload_run.fern")
	copySelfHostDriver(t, dir, "wasm_modload_run.fern")
	const types = `pub struct Node { value: i32 }
pub function make(): Node { return Node { value: 7 }; }
`
	cases := []struct{ name, api, entry string }{
		{"callable", `import "./types";
function apply(node: types.Node, callback: (types.Node) => types.Node): types.Node { return callback(node); }
pub function run(callback: (types.Node) => types.Node): i32 {
    var node = apply(types.Node { value: 7 }, (n: types.Node): types.Node => callback(n));
    return node.value;
}`, `import "./api"; import "./types";
function main(): i32 { return api.run((n: types.Node): types.Node => n); }`},
		{"nominal-array", `import "./types";
function apply(callback: () => i32): i32 { return callback(); }
pub function run(nodes: types.Node[]): i32 { return apply((): i32 => nodes[0].value); }
`, `import "./api"; import "./types";
function main(): i32 { return api.run([types.Node { value: 7 }]); }`},
		{"inferred-call-result", `import "./types";
function apply(callback: () => i32): i32 { return callback(); }
pub function run(): i32 { var node = types.make(); return apply((): i32 => node.value); }
`, `import "./api"; function main(): i32 { return api.run(); }`},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			driverName := "asm_modload_run.fern"
			if target == "wasm32-wasi" {
				driverName = "wasm_modload_run.fern"
			}
			driver := buildSelfHostBin(t, gcc, dir, driverName, strings.TrimSuffix(driverName, ".fern"))
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					proj := t.TempDir()
					entry := filepath.Join(proj, "main.fern")
					for name, src := range map[string]string{"types.fern": types, "api.fern": tc.api, "main.fern": tc.entry} {
						if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					if _, code := runFixtureInterp(t, entry, ""); code != 7 {
						t.Fatalf("interpreter = %d, want 7", code)
					}
					drive := func(args ...string) []byte {
						t.Helper()
						args = append([]string{entry}, args...)
						if target != "wasm32-wasi" {
							args = append(args, "-target", target)
						}
						cmd := runX86_64Bin(runner, driver, args...)
						var stderr bytes.Buffer
						cmd.Stderr = &stderr
						out, err := cmd.Output()
						if err != nil {
							t.Fatalf("driver %v: %v\n%s", args, err, stderr.String())
						}
						return out
					}
					if count := strings.TrimSpace(string(drive("-per-module-count"))); count != "3" {
						t.Fatalf("module count = %q, want 3 independent units", count)
					}
					var run *exec.Cmd
					if target == "wasm32-wasi" {
						if _, err := exec.LookPath("wasmtime"); err != nil {
							t.Fatal("wasmtime required for the per-module capture test")
						}
						cache := filepath.Join(proj, "cache")
						if err := os.Mkdir(cache, 0o755); err != nil {
							t.Fatal(err)
						}
						for i := range 3 {
							drive("-per-module-emit", strconv.Itoa(i), "-cache-dir", cache)
						}
						wat := filepath.Join(proj, "program.wat")
						if err := os.WriteFile(wat, drive("-link", "-cache-dir", cache), 0o644); err != nil {
							t.Fatal(err)
						}
						run = exec.Command("wasmtime", "run", wat)
					} else {
						var needs []string
						for _, need := range strings.Fields(string(drive("-per-module-needs"))) {
							needs = append(needs, "-extra-need", need)
						}
						var units []string
						for i := range 3 {
							unit := filepath.Join(proj, "unit"+strconv.Itoa(i)+".s")
							args := append([]string{"-per-module-emit", strconv.Itoa(i)}, needs...)
							if err := os.WriteFile(unit, drive(args...), 0o644); err != nil {
								t.Fatal(err)
							}
							units = append(units, unit)
						}
						linker := gcc
						var armrunner string
						if target == "arm64-linux" {
							linker, armrunner = arm64Tooling(t)
						}
						bin := filepath.Join(proj, "program")
						args := append([]string{"-static", "-nostdlib", "-no-pie"}, units...)
						args = append(args, "-o", bin)
						if out, err := exec.Command(linker, args...).CombinedOutput(); err != nil {
							t.Fatalf("link: %v\n%s", err, out)
						}
						run = runX86_64Bin(runner, bin)
						if target == "arm64-linux" {
							run = runArm64Bin(armrunner, bin)
						}
					}
					out, err := run.CombinedOutput()
					if run.ProcessState == nil || !run.ProcessState.Exited() || run.ProcessState.ExitCode() != 7 {
						t.Fatalf("runtime: %v, want exit 7\n%s", err, out)
					}
				})
			}
		})
	}
}
