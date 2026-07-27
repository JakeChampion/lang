package e2eselfhost

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestSelfHostWasmWholeCompilerShardedLink is the payoff for #5508: the ENTIRE
// self-host wasm compiler (15 modules, incl. irlower at ~894 funcs) links into
// one valid wasm module — with the oversized module emitted in FUNCTION WINDOWS
// across separate processes so no single lowering exhausts the bump arena.
//
// This is the wasm analogue of the asm whole-compiler per-module link test, and
// the first end-to-end proof that per-module wasm emit scales past the toy cases
// to the compiler itself. wasm-tools validate is the structural bar — a module
// that assembled with mismatched namespaces, a missing runtime helper, or a
// dangling funcref fails it — and the linked compiler is then RUN (see
// runShardedCompiler): validating proves well-formedness, not that the windows
// compute the right thing.
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
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern",
		"flatten.fern", "modloader.fern", "fern_toml.fern", "builtins.fern",
		"wasm_objfile.fern", "wasm_modload_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_modload_run.fern", "wasm_modload_run")
	entryPath := filepath.Join(dir, "wasm_modload_run.fern")

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

	// Emit each module in FUNCTION WINDOWS, each its own process (fresh arena).
	// Windows run on a bounded worker pool so the per-process parse floors
	// overlap — the whole-compiler emit is CI-runnable this way, not a ~40-min
	// serial slog (mirrors the asm sibling's runPmEmitJobs). A window that still
	// OOMs (137) is halved by its own worker (rare; the static window already
	// clears the arena). Each job records its own plan lines; the plan is
	// assembled in job order so it is deterministic regardless of interleaving.
	const window = 150
	// 2 workers, not 3: each per-module wasm emit's peak RSS is ~2x the asm
	// sibling's pmEmitWorkers=3 (heavier whole-program view + wasm emit), so 3
	// concurrent window emits peaked at ~14.5 GB — over a 16 GB CI runner's
	// headroom — OOMing the selfhost shard that hosts the capstone (exit 143, no
	// assertion failure) reproducibly at the irlower windows. 2 keeps the peak
	// ~10 GB at ~1.5x wall-clock, still well under the 18m test budget.
	const workers = 2
	isOOM := func(err error) bool {
		var ee *exec.ExitError
		return errors.As(err, &ee) && ee.ExitCode() == 137
	}
	type job struct {
		idx, lo, hi int
		lines       []string
		err         error
	}
	var jobs []*job
	for i := range n {
		if counts[i] <= window {
			jobs = append(jobs, &job{idx: i, lo: 0, hi: counts[i]})
			continue
		}
		for lo := 0; lo < counts[i]; lo += window {
			jobs = append(jobs, &job{idx: i, lo: lo, hi: min(lo+window, counts[i])})
		}
	}
	// emitWin emits [lo,hi), self-splitting on OOM; appends plan lines to *out.
	var emitWin func(idx, lo, hi int, out *[]string) error
	emitWin = func(idx, lo, hi int, out *[]string) error {
		rng := strconv.Itoa(lo) + ":" + strconv.Itoa(hi)
		_, se, err := drive(t, "-per-module-emit", strconv.Itoa(idx), "-func-range", rng, "-cache-dir", cacheDir)
		if err == nil {
			*out = append(*out, strconv.Itoa(idx)+" "+strconv.Itoa(lo)+" "+strconv.Itoa(hi))
			return nil
		}
		if !isOOM(err) {
			return fmt.Errorf("emit module %d [%d,%d) failed (not OOM): %v\n%s", idx, lo, hi, err, se)
		}
		if hi-lo <= 1 {
			return fmt.Errorf("emit module %d single-func window [%d,%d) still OOMed", idx, lo, hi)
		}
		mid := lo + (hi-lo)/2
		if e := emitWin(idx, lo, mid, out); e != nil {
			return e
		}
		return emitWin(idx, mid, hi, out)
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j *job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			j.err = emitWin(j.idx, j.lo, j.hi, &j.lines)
		}(j)
	}
	wg.Wait()
	var plan strings.Builder
	for _, j := range jobs {
		if j.err != nil {
			t.Fatalf("%v", j.err)
		}
		for _, ln := range j.lines {
			plan.WriteString(ln + "\n")
		}
	}

	planPath := filepath.Join(dir, "plan.txt")
	if err := os.WriteFile(planPath, []byte(plan.String()), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
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
	t.Logf("whole-compiler wasm link OK: %d modules, %d units, %d bytes WAT, validated", n, len(jobs), len(wat))
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
// function windows across as many processes — and the string literal in the leaf
// module makes the data-section/base wiring load-bearing rather than incidental.
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
