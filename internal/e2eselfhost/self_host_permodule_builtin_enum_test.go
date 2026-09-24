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

// A module that matches on a builtin enum's variants lowers in a per-module
// wasm build. The builtin enums are declared by no module, and the wasm
// per-module driver built its struct table from the modules alone, so any
// `match` on IoError's variants bailed with no emitter to fall back to — found
// when the self-host compiler's own modloader first matched one, which failed
// TestSelfHostWasmWholeCompilerShardedLink.
const perModuleBuiltinEnumLib = `pub function code(e: IoError): i32 {
    match (e) {
        NotFound(_) => { return 1; },
        InvalidUtf8(_) => { return 2; },
        Other(_, msg) => { return msg.len(); },
        _ => { return 9; },
    }
    return 9;
}
`

const perModuleBuiltinEnumEntry = `import "./lib";
function main(): i32 {
    return lib.code(NotFound("a")) * 100 + lib.code(InvalidUtf8("b")) * 10 + lib.code(Other("c", "xyz"));
}
`

func TestSelfHostPerModuleBuiltinEnumMatchWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm per-module builtin-enum e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm per-module builtin-enum e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the file-loading driver resolves sibling imports by host path, so it runs only natively")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_modload_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_modload_run.fern", "wasm_modload_run")

	proj := filepath.Join(dir, "builtin_enum")
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "lib.fern"), []byte(perModuleBuiltinEnumLib), 0o644); err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(proj, "entry.fern")
	if err := os.WriteFile(entryPath, []byte(perModuleBuiltinEnumEntry), 0o644); err != nil {
		t.Fatal(err)
	}
	drive := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(driverBin, append([]string{entryPath}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("driver %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}
	nmod, err := strconv.Atoi(strings.TrimSpace(drive("-per-module-count")))
	if err != nil || nmod != 2 {
		t.Fatalf("module count != 2, so lib is not its own unit")
	}
	for i := 0; i < nmod; i++ {
		drive("-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir)
	}
	out, err := exec.Command(driverBin, entryPath, "-link", "-cache-dir", cacheDir).Output()
	if err != nil {
		t.Fatalf("-link failed: %v", err)
	}
	watPath := filepath.Join(proj, "prog.wat")
	if err := os.WriteFile(watPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	corePath := filepath.Join(proj, "prog.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("the linked module does not parse: %v\n%s", err, out)
	}
	got := 0
	if out, runErr := exec.Command(wasmtime, "run", corePath).CombinedOutput(); runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			t.Fatalf("wasmtime run: %v\n%s", runErr, out)
		}
		got = ee.ExitCode()
	}
	if got != 123 {
		t.Errorf("linked wasm units exited %d, want 123 (1, 2 and 3 from the three variants)", got)
	}
}
