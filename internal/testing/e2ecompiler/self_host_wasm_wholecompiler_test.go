package e2ecompiler

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostWasmWholeCompilerShardedLink is the main gate for #5508: the ENTIRE
// self-host wasm compiler links into one valid wasm module from per-unit
// objects, with each large module cut into FUNCTION WINDOWS, a unit apiece.
//
// This is the wasm analogue of the asm whole-compiler per-module link test, and
// the first end-to-end proof that per-module wasm emit scales past the toy cases
// to the compiler itself. wasm-tools validate is the structural bar — a module
// that assembled with mismatched namespaces, a missing runtime helper, or a
// dangling funcref fails it — and the linked compiler is then RUN (see
// runShardedCompiler): validating proves well-formedness, not that the windows
// compute the right thing.
//
// Runs in its own CI job (wasm-wholecompiler-link-x86_64), not in an LPT shard
// (#6667).
func TestSelfHostWasmWholeCompilerShardedLink(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-compiler sharded link is heavy; skipped in -short")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping whole-compiler sharded link")
	}
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping whole-compiler sharded link")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "flatten.fern", "modloader.fern", "fern_toml.fern", "builtins.fern", "drivers/wasm_objfile.fern", "drivers/wasm_modload_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_modload_run.fern", "wasm_modload_run")
	entryPath := filepath.Join(dir, "drivers/wasm_modload_run.fern")
	// The compiler imports core/map, which resolves beside the entry.
	copyStdlibTree(t, dir)

	drive := func(t *testing.T, args ...string) (string, string, error) {
		t.Helper()
		full := append([]string{entryPath}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		var so, se bytes.Buffer
		cmd.Stdout = &so
		cmd.Stderr = &se
		err := cmd.Run()
		return so.String(), se.String(), err
	}

	cacheDir := filepath.Join(dir, "cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	cntOut, _, err := drive(t, "-per-module-count")
	if err != nil {
		t.Fatalf("-per-module-count: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(cntOut))
	if err != nil || n < 10 {
		t.Fatalf("module count = %q (want >= 10)", cntOut)
	}

	fcOut, _, err := drive(t, "-per-module-func-counts")
	if err != nil {
		t.Fatalf("-per-module-func-counts: %v", err)
	}
	var counts []int
	for _, f := range strings.Fields(strings.TrimSpace(fcOut)) {
		c, cerr := strconv.Atoi(f)
		if cerr != nil {
			t.Fatalf("func-count %q: %v", f, cerr)
		}
		counts = append(counts, c)
	}
	if len(counts) != n {
		t.Fatalf("func-counts returned %d lines, want %d", len(counts), n)
	}

	// The plan cuts each module into FUNCTION WINDOWS, so the link assembles
	// many namespaced units per large module. One process emits every unit: the
	// typed lowering is whole-program, so a process per window would each lower
	// the whole compiler again.
	const window = 150
	var plan strings.Builder
	units := 0
	for i := range n {
		for lo := 0; lo < max(counts[i], 1); lo += window {
			plan.WriteString(strconv.Itoa(i) + " " + strconv.Itoa(lo) + " " + strconv.Itoa(min(lo+window, counts[i])) + "\n")
			units++
		}
	}

	planPath := filepath.Join(dir, "plan.txt")
	if err := os.WriteFile(planPath, []byte(plan.String()), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	// A host OOM kill (137) on a loaded runner is retried once; the units the
	// killed run wrote are cache hits the second time. The arena trap (125) is
	// deterministic, so it is not retried.
	_, se, err := drive(t, "-per-module-emit-all", "-plan", planPath, "-cache-dir", cacheDir)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 137 {
		t.Logf("-per-module-emit-all was killed (137); retrying once")
		_, se, err = drive(t, "-per-module-emit-all", "-plan", planPath, "-cache-dir", cacheDir)
	}
	if err != nil {
		t.Fatalf("-per-module-emit-all failed: %v\n%s", err, se)
	}
	wat, se, err := drive(t, "-link", "-plan", planPath, "-cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("-link failed: %v\n%s", err, se)
	}
	if len(wat) == 0 {
		t.Fatal("-link produced no module text")
	}
	watPath := filepath.Join(dir, "whole.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "whole.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse of the whole-compiler link failed: %v\n%s", err, out)
	}
	if out, err := exec.Command(wasmtools, "validate", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate of the whole-compiler link failed: %v\n%s", err, out)
	}
	// Report the CORE MODULE size, not just the WAT text: the binary is what a
	// consumer would ship, and the two differ by several-fold. #6643 needed this
	// number and had to re-derive it — docs/PLAYGROUND-SELFHOST-WASM.md.
	coreBytes := int64(-1)
	if fi, err := os.Stat(corePath); err == nil {
		coreBytes = fi.Size()
	}
	t.Logf("whole-compiler wasm link OK: %d modules, %d units, %d bytes WAT, %d bytes core module, validated", n, units, len(wat), coreBytes)
	runShardedCompiler(t, wasmtime, wasmtools, dir, corePath)
}

// runShardedCompiler runs the linked whole-compiler module. Validation above
// proves the module is well-FORMED; it says nothing about whether the windows
// compute the right thing. A shard whose $__str_base or $__fn_base was assembled
// against another unit's literals validates perfectly and reads the wrong string
// or calls the wrong funcref — exactly the failure mode sharding introduces, and
// exactly the one validate cannot see.
//
// The linked module IS the wasm compiler, so the sharpest available exercise is
// to make it compile something: drive the wasm-hosted driver through the same
// count → emit → link cycle the Go harness just drove natively, over a small
// two-module program, then run the module IT produced. The program's answer
// (14 = len("sharded") + 7) comes back only if the lexer, parser, module
// resolution, IR lowering and wasm emit all still work after being cut into
// function windows — and the string literal in the leaf
// module makes the data-section/base wiring essential rather than incidental.
func runShardedCompiler(t *testing.T, wasmtime, wasmtools, dir, compiler string) {
	t.Helper()
	proj := filepath.Join(dir, "proj")
	if err := os.MkdirAll(filepath.Join(proj, "cache"), 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	for _, f := range []struct{ name, src string }{
		{"leaf.fern", "pub function leaf_tag(): string { return \"sharded\"; }\n"},
		{"prog.fern", "import \"./leaf\";\nfunction main(): i32 { return leaf.leaf_tag().len() + 7; }\n"},
	} {
		if err := os.WriteFile(filepath.Join(proj, f.name), []byte(f.src), 0o644); err != nil {
			t.Fatalf("write %s: %v", f.name, err)
		}
	}
	const want = 14

	// The guest sees proj as its only preopen, so paths are relative to it —
	// `prog.fern` is the entry and `cache` the object dir, both inside proj.
	drive := func(args ...string) string {
		t.Helper()
		full := append([]string{"run", "--dir", proj + "::/", compiler, "prog.fern"}, args...)
		cmd := exec.Command(wasmtime, full...)
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		if err := cmd.Run(); err != nil {
			t.Fatalf("wasm-hosted compiler %v failed: %v\n%s", args, err, se.String())
		}
		return so.String()
	}

	if got := strings.TrimSpace(drive("-per-module-count")); got != "2" {
		t.Fatalf("wasm-hosted -per-module-count = %q, want 2 — the linked compiler mis-resolved the program", got)
	}
	for i := range 2 {
		drive("-per-module-emit", strconv.Itoa(i), "-cache-dir", "cache")
	}
	out := drive("-link", "-cache-dir", "cache")
	if len(out) == 0 {
		t.Fatal("wasm-hosted -link produced no module text")
	}

	watPath := filepath.Join(dir, "hosted.wat")
	if err := os.WriteFile(watPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write hosted wat: %v", err)
	}
	binPath := filepath.Join(dir, "hosted.wasm")
	if o, err := exec.Command(wasmtools, "parse", watPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse of the wasm-hosted compiler's output failed: %v\n%s", err, o)
	}
	got := 0
	if err := exec.Command(wasmtime, "run", binPath).Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run the wasm-hosted compiler's output: %v", err)
		}
		got = ee.ExitCode()
	}
	if got != want {
		t.Fatalf("program compiled by the sharded-linked compiler returned %d, want %d", got, want)
	}
	t.Logf("sharded-linked whole compiler runs: compiled a 2-module program to wasm, answer %d", got)
}
