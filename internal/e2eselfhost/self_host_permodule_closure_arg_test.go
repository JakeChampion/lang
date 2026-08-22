package e2eselfhost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A CAPTURING lambda handed to a SIBLING module's fn-typed parameter is the
// shape #7215 broke on. The lift pass decides whether such an argument is
// env-boxed by looking the CALLEE up, so a per-module build that lifts a part
// against only its own functions cannot see that `lib.apply` takes a fn — the
// lambda stays raw, and lowering then emits `const_func(<fd>$clo)` naming a
// closure body no lift ever hoisted. The whole-program build never noticed (one
// module, every callee visible); every per-unit link failed on
// `undefined reference to __fn_<fd>$clo`.
//
// The cases below walk the positions that share that code path — the reported
// assignment RHS, plus var-init, return, nested-call, if-condition, an escaping
// closure built by a local factory, and the local-binding control that already
// worked — so a regression in any one of them fails by name.
const perModuleClosureArgLib = `pub function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
`

var perModuleClosureArgCases = []struct {
	name  string
	entry string
	want  int
}{
	// The reported shape: the call is the value of an ASSIGNMENT.
	{"assign-arg", `import "./lib";
function run(n: i32): i32 { var acc: i32 = 0; acc = lib.apply(function (y: i32): i32 { return y + n; }, 4); return acc; }
function main(): i32 { return run(3); }`, 7},
	// The same, accumulating in a loop — the form the issue's repro used, where
	// the capture is read once per iteration.
	{"assign-arg-loop", `import "./lib";
function run(n: i32, k: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < k) { acc = lib.apply(function (y: i32): i32 { return y + n; }, acc); i = i + 1; } return acc; }
function main(): i32 { return run(3, 4); }`, 12},
	// var-init position.
	{"var-arg", `import "./lib";
function run(n: i32): i32 { var acc: i32 = lib.apply(function (y: i32): i32 { return y + n; }, 4); return acc; }
function main(): i32 { return run(3); }`, 7},
	// return position — the one the issue expected to work.
	{"return-arg", `import "./lib";
function run(n: i32): i32 { return lib.apply(function (y: i32): i32 { return y + n; }, 4); }
function main(): i32 { return run(3); }`, 7},
	// Nested: the argument of the outer call is itself a call carrying one.
	{"nested-arg", `import "./lib";
function run(n: i32): i32 { var acc: i32 = 0; acc = lib.apply(function (y: i32): i32 { return y + n; }, lib.apply(function (z: i32): i32 { return z * 2; }, 5)); return acc; }
function main(): i32 { return run(3); }`, 13},
	// if-condition position.
	{"if-cond-arg", `import "./lib";
function run(n: i32): i32 { if (lib.apply(function (y: i32): i32 { return y + n; }, 4) > 5) { return 7; } return 1; }
function main(): i32 { return run(3); }`, 7},
	// The closure is a RETURNED value first, then handed across the boundary —
	// the escaping-closure hoist feeding a cross-module fn param.
	{"returned-closure-arg", `import "./lib";
function mk(n: i32): (i32) => i32 { return function (y: i32): i32 { return y + n; }; }
function run(n: i32): i32 { return lib.apply(mk(n), 4); }
function main(): i32 { return run(3); }`, 7},
	// Control: bound to a local first. This one already linked before the fix,
	// because the var-binding lift boxes a value-used lambda without consulting
	// the callee at all.
	{"local-first", `import "./lib";
function run(n: i32): i32 { var f: (i32) => i32 = function (y: i32): i32 { return y + n; }; var acc: i32 = 0; acc = lib.apply(f, 4); return acc; }
function main(): i32 { return run(3); }`, 7},
}

// writeClosureArgProject writes one case's two-module project and returns the
// entry path.
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

// TestSelfHostPerModuleClosureArgLinkX86_64 is the gate for #7215: each case is
// emitted as SEPARATE translation units and linked. The link is the assertion —
// a `$clo` reference with no definition behind it is invisible until then — and
// the exit code proves the closure the units agreed on actually computes.
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

// TestSelfHostWholeProgramClosureArgX86_64 runs the same cases through the
// MERGED whole-program emit. That build never failed on #7215 — one module, so
// every callee is in the lift's view — which is exactly why it is worth pinning:
// it is the oracle the per-unit leg above has to agree with, and the leg that
// would notice if the cross-module boxing changed the answer rather than only
// the symbol set.
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

// TestSelfHostPerModuleClosureArgLinkWasm is the wasm leg of the same gate. The
// wasm failure mode is not a linker error but a missing elem-table entry —
// `unknown func: failed to find name $<fd>$clo` — so the assertion is that the
// linked module parses at all, then that it computes.
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
	copySelfHostDriver(t, dir, "wasm_modload_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_modload_run.fern", "wasm_modload_run")

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
