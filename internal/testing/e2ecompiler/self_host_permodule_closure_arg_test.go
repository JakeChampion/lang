package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const perModuleClosureArgLib = `pub function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
`

var perModuleClosureArgCases = []struct {
	name  string
	entry string
	want  int
}{
	{"assign-arg", `import "./lib";
function run(n: i32): i32 { let acc: i32 = 0; acc = lib.apply((y: i32): i32 => { return y + n; }, 4); return acc; }
function main(): i32 { return run(3); }`, 7},
	{"assign-arg-loop", `import "./lib";
function run(n: i32, k: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < k) { acc = lib.apply((y: i32): i32 => { return y + n; }, acc); i = i + 1; } return acc; }
function main(): i32 { return run(3, 4); }`, 12},
	{"var-arg", `import "./lib";
function run(n: i32): i32 { let acc: i32 = lib.apply((y: i32): i32 => { return y + n; }, 4); return acc; }
function main(): i32 { return run(3); }`, 7},
	{"return-arg", `import "./lib";
function run(n: i32): i32 { return lib.apply((y: i32): i32 => { return y + n; }, 4); }
function main(): i32 { return run(3); }`, 7},
	{"nested-arg", `import "./lib";
function run(n: i32): i32 { let acc: i32 = 0; acc = lib.apply((y: i32): i32 => { return y + n; }, lib.apply((z: i32): i32 => { return z * 2; }, 5)); return acc; }
function main(): i32 { return run(3); }`, 13},
	{"if-cond-arg", `import "./lib";
function run(n: i32): i32 { if (lib.apply((y: i32): i32 => { return y + n; }, 4) > 5) { return 7; } return 1; }
function main(): i32 { return run(3); }`, 7},
	{"returned-closure-arg", `import "./lib";
function mk(n: i32): (i32) => i32 { return (y: i32): i32 => { return y + n; }; }
function run(n: i32): i32 { return lib.apply(mk(n), 4); }
function main(): i32 { return run(3); }`, 7},
	{"local-first", `import "./lib";
function run(n: i32): i32 { let f: (i32) => i32 = (y: i32): i32 => { return y + n; }; let acc: i32 = 0; acc = lib.apply(f, 4); return acc; }
function main(): i32 { return run(3); }`, 7},
}

func writeClosureArgProject(t *testing.T, root, name, entry string) string {
	t.Helper()
	proj := filepath.Join(root, "cloarg_"+name)
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", proj, err)
	}
	if err := os.WriteFile(filepath.Join(proj, "lib.fern"), []byte(perModuleClosureArgLib), 0o644); err != nil {
		t.Fatalf("write lib.fern: %v", err)
	}
	entryPath := filepath.Join(proj, "entry.fern")
	if err := os.WriteFile(entryPath, []byte(entry+"\n"), 0o644); err != nil {
		t.Fatalf("write entry.fern: %v", err)
	}
	return entryPath
}

func TestSelfHostPerModuleClosureArgLinkX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the file-loading driver resolves sibling imports by host path, so it runs only natively")
	}
	dir, mmr := buildConcatDriver(t, gcc)

	for _, tc := range perModuleClosureArgCases {
		t.Run(tc.name, func(t *testing.T) {
			entryPath := writeClosureArgProject(t, dir, tc.name, tc.entry)

			cnt, err := exec.Command(mmr, entryPath, "-per-module-count").Output()
			if err != nil {
				t.Fatalf("-per-module-count: %v", err)
			}
			nmod, err := strconv.Atoi(strings.TrimSpace(string(cnt)))
			if err != nil || nmod != 2 {
				t.Fatalf("-per-module-count = %q, want 2 (entry + lib) — a single-module build would not exercise the cross-module lift at all", cnt)
			}

			needs, err := exec.Command(mmr, entryPath, "-per-module-needs").Output()
			if err != nil {
				t.Fatalf("-per-module-needs: %v", err)
			}
			var needArgs []string
			for _, ln := range strings.Split(string(needs), "\n") {
				if s := strings.TrimSpace(ln); s != "" {
					needArgs = append(needArgs, "-extra-need", s)
				}
			}

			var units []string
			for i := 0; i < nmod; i++ {
				args := append([]string{entryPath, "-per-module-emit", strconv.Itoa(i)}, needArgs...)
				out, err := exec.Command(mmr, args...).CombinedOutput()
				if err != nil {
					t.Fatalf("unit %d did not emit: %v\n%s", i, err, out)
				}
				p := filepath.Join(filepath.Dir(entryPath), "unit"+strconv.Itoa(i)+".s")
				if err := os.WriteFile(p, out, 0o644); err != nil {
					t.Fatalf("write unit %d: %v", i, err)
				}
				units = append(units, p)
			}

			binPath := filepath.Join(filepath.Dir(entryPath), "prog")
			linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, units...)
			linkArgs = append(linkArgs, "-o", binPath)
			if out, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
				t.Fatalf("linking the per-unit build failed: %v\n%s", err, out)
			}
			cmd := exec.Command(binPath)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("per-unit build exited %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSelfHostWholeProgramClosureArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the file-loading driver resolves sibling imports by host path, so it runs only natively")
	}
	dir, mmr := buildConcatDriver(t, gcc)

	for _, tc := range perModuleClosureArgCases {
		t.Run(tc.name, func(t *testing.T) {
			entryPath := writeClosureArgProject(t, dir, "whole_"+tc.name, tc.entry)
			asm, err := exec.Command(mmr, entryPath).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("merged emit failed: %v (len=%d)", err, len(asm))
			}
			binPath := buildBin(t, gcc, dir, "whole_"+tc.name, string(asm))
			cmd := exec.Command(binPath)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("whole-program build exited %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSelfHostPerModuleClosureArgLinkWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm per-module closure-arg e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm per-module closure-arg e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the file-loading driver resolves sibling imports by host path, so it runs only natively")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_modload_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_modload_run.fern", "wasm_modload_run")

	for _, tc := range perModuleClosureArgCases {
		t.Run(tc.name, func(t *testing.T) {
			entryPath := writeClosureArgProject(t, dir, "wasm_"+tc.name, tc.entry)
			cacheDir := filepath.Join(filepath.Dir(entryPath), "cache")
			if err := os.Mkdir(cacheDir, 0o755); err != nil {
				t.Fatalf("mkdir cache: %v", err)
			}
			drive := func(args ...string) string {
				t.Helper()
				out, err := exec.Command(driverBin, append([]string{entryPath}, args...)...).Output()
				if err != nil {
					t.Fatalf("driver %v failed: %v", args, err)
				}
				return string(out)
			}
			nmod, err := strconv.Atoi(strings.TrimSpace(drive("-per-module-count")))
			if err != nil || nmod != 2 {
				t.Fatalf("module count != 2, so the cross-module lift is not exercised")
			}
			for i := 0; i < nmod; i++ {
				drive("-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir)
			}
			wat := drive("-link", "-cache-dir", cacheDir)

			watPath := filepath.Join(filepath.Dir(entryPath), "prog.wat")
			if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			corePath := filepath.Join(filepath.Dir(entryPath), "prog.wasm")
			if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
				t.Fatalf("the linked wasm module does not parse (a $clo with no definition shows up here): %v\n%s", err, out)
			}
			got := 0
			if out, runErr := exec.Command(wasmtime, "run", corePath).CombinedOutput(); runErr != nil {
				var ee *exec.ExitError
				if !errors.As(runErr, &ee) {
					t.Fatalf("wasmtime run: %v\n%s", runErr, out)
				}
				got = ee.ExitCode()
			}
			if got != tc.want {
				t.Errorf("linked wasm units exited %d, want %d", got, tc.want)
			}
		})
	}
}
