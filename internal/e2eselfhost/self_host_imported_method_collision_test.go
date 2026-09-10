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

// importedMethodCollisionCases pin an imported struct METHOD against a free
// function of the same spelling in the importing module (#8997).
//
// The self-host var-binding lowering classified `var t: Counter = c.release()`
// by looking `release` up in the array-returning-function registry under its
// BARE name, gated only on "the receiver is a struct". The registry keys every
// free function by that same bare name, so an unrelated
// `release(i32[], i32): i32[]` in the importing module marked the Counter-typed
// slot as an array and the next method call on it refused to lower
// ("release_zero (did not lower: call `.value`)"). The array-returning method
// registration is also keyed "<BaseType>.<method>", and the expression-level
// check (expr_is_arr_src) has always used that qualified key; the binding path
// now does the same.
//
// Both cases keep the name collision — it IS the regression — and both need the
// method to arrive by IMPORT, since a whole-program registry is what carries
// the importing module's free function into the imported module's lowering.
var importedMethodCollisionCases = []struct {
	name  string
	files map[string]string
	want  int
}{
	// The #8997 repro: a struct-returning method shadowed by an
	// array-returning free function of the same name.
	{"struct-method-vs-free-arr-fn", map[string]string{
		"counter.fern": `pub struct Counter { n: i32 }

pub function (c: Counter) value(): i32 { return c.n; }

pub function (c: Counter) release(): Counter { return Counter { n: c.n }; }

pub function (c: Counter) release_zero(): Counter {
    var t: Counter = c.release();
    return Counter { n: t.value() };
}
`,
		"main.fern": `import "./counter";

function release(xs: i32[], n: i32): i32[] { return xs.append(n); }

function main(): i32 {
    var c: counter.Counter = counter.Counter { n: 42 };
    var r: counter.Counter = c.release_zero();
    var xs: i32[] = release([1], 2);
    return r.value() + xs.len() - 2;
}
`}, 42},

	// The control the qualified key must not lose: here the METHOD is the
	// array-returning one and the free function is not, so the binding must
	// still classify as an array — `for y in ys` requires the slot flag, not
	// just the expression classifier.
	{"arr-ret-method-vs-free-scalar-fn", map[string]string{
		"bag.fern": `pub struct Bag { xs: i32[] }

pub function (b: Bag) take(): i32[] { return b.xs; }

pub function (b: Bag) total(): i32 {
    var ys = b.take();
    var t: i32 = 0;
    for y in ys { t = t + y; }
    return t;
}
`,
		"main.fern": `import "./bag";

function take(n: i32): i32 { return n; }

function main(): i32 {
    var b: bag.Bag = bag.Bag { xs: [20, 20] };
    return b.total() + take(2);
}
`}, 42},
}

// writeImportedMethodCollisionProject lays the case's modules out in a fresh
// directory and returns the entry path.
func writeImportedMethodCollisionProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return filepath.Join(dir, "main.fern")
}

// TestSelfHostImportedMethodCollisionX86_64 — the x86-64 IR path.
func TestSelfHostImportedMethodCollisionX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	if len(runner) != 0 {
		t.Skip("the file-loading driver resolves sibling imports by host path, so it runs only natively")
	}
	dir := t.TempDir()
	for _, tc := range importedMethodCollisionCases {
		t.Run(tc.name, func(t *testing.T) {
			entry := writeImportedMethodCollisionProject(t, tc.files)
			asm := string(runDriverFile(t, runner, driverBin, entry))
			bin := buildBin(t, gcc, dir, "imported_method_collision_"+tc.name, asm)
			cmd := binCmd(runner, bin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostImportedMethodCollisionArm64 — the arm64 IR path.
func TestSelfHostImportedMethodCollisionArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	if len(x86runner) != 0 {
		t.Skip("the file-loading driver resolves sibling imports by host path, so it runs only natively")
	}
	dir := t.TempDir()
	for _, tc := range importedMethodCollisionCases {
		t.Run(tc.name, func(t *testing.T) {
			entry := writeImportedMethodCollisionProject(t, tc.files)
			asm := string(runDriverFile(t, x86runner, driverBin, entry, "-target", "arm64-linux"))
			bin := buildBinArm64(t, arm64gcc, dir, "imported_method_collision_"+tc.name, asm)
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostImportedMethodCollisionWasm — the wasm IR path, which reaches the
// same lowering through the per-module emit + link driver rather than a merged
// whole-program emit.
func TestSelfHostImportedMethodCollisionWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm imported-method collision e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the file-loading driver resolves sibling imports by host path, so it runs only natively")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_modload_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_modload_run.fern", "wasm_modload_run")

	for _, tc := range importedMethodCollisionCases {
		t.Run(tc.name, func(t *testing.T) {
			entry := writeImportedMethodCollisionProject(t, tc.files)
			proj := filepath.Dir(entry)
			cache := filepath.Join(proj, "cache")
			if err := os.Mkdir(cache, 0o755); err != nil {
				t.Fatalf("mkdir cache: %v", err)
			}
			drive := func(args ...string) string {
				t.Helper()
				cmd := exec.Command(driverBin, append([]string{entry}, args...)...)
				out, err := cmd.Output()
				if err != nil {
					var stderr []byte
					var ee *exec.ExitError
					if errors.As(err, &ee) {
						stderr = ee.Stderr
					}
					t.Fatalf("wasm driver %v: %v\n%s", args, err, stderr)
				}
				return string(out)
			}
			nmod, err := strconv.Atoi(strings.TrimSpace(drive("-per-module-count")))
			if err != nil || nmod != 2 {
				t.Fatalf("module count = %d (err %v), want the 2 units the import split gives", nmod, err)
			}
			for i := 0; i < nmod; i++ {
				drive("-per-module-emit", strconv.Itoa(i), "-cache-dir", cache)
			}
			wat := filepath.Join(proj, "prog.wat")
			if err := os.WriteFile(wat, []byte(drive("-link", "-cache-dir", cache)), 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command(wasmtime, "run", wat)
			out, runErr := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s: %v\n%s", tc.name, runErr, out)
			}
			if got := run.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d\n%s", tc.name, got, tc.want, out)
			}
		})
	}
}
