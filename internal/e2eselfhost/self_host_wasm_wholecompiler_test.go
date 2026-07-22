package e2eselfhost

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

// TestSelfHostWasmWholeCompilerShardedLink is the payoff for #5508: the ENTIRE
// self-host wasm compiler (15 modules, incl. irlower at ~894 funcs) links into
// one valid wasm module — with the oversized module emitted in FUNCTION WINDOWS
// across separate processes so no single lowering exhausts the bump arena.
//
// This is the wasm analogue of the asm whole-compiler per-module link test, and
// the first end-to-end proof that per-module wasm emit scales past the toy cases
// to the compiler itself. It does not run the linked module (that would need the
// compiler's argv/fs environment); wasm-tools validate is the bar — a module
// that assembled with mismatched namespaces, a missing runtime helper, or a
// dangling funcref would fail it.
func TestSelfHostWasmWholeCompilerShardedLink(t *testing.T) {
	if os.Getenv("FERN_WHOLE_COMPILER") == "" {
		t.Skip("whole-compiler sharded link is heavy (~40 min, sequential 15-module emit); set FERN_WHOLE_COMPILER=1. CI correctness is covered by TestSelfHostWasmFuncRangeShard (sharding) + the fixpoints (the RC-consistency lowering).")
	}
	// NOTE: this capstone does not yet reach a validated module. The sharding
	// and the RC-helper metadata/emit fix (this PR, #5508) let it get PAST those
	// two failures, but the whole-compiler emit then hits the NEXT missing-feature
	// layer: the wasm-IR path does not lower the Cell primitives that asmcore /
	// wasm use, so link fails with `unknown func $cell_new` (#5510). The test is
	// kept as the driver for closing that and any further gaps; each fix peels one
	// layer. It is expected to FAIL on #5510 until Cell lowering lands.
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping whole-compiler sharded link")
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

	// Emit each module. A module over the window threshold is emitted in
	// [lo,hi) slices, each its own process (fresh arena); one under it emits
	// whole. If a window still OOMs (137), halve it — the safety net under the
	// static threshold, mirroring the asm orchestrator's recursive split.
	const window = 150
	isOOM := func(err error) bool {
		var ee *exec.ExitError
		return errors.As(err, &ee) && ee.ExitCode() == 137
	}
	var plan strings.Builder
	var emitWindow func(idx, lo, hi int)
	emitWindow = func(idx, lo, hi int) {
		rng := strconv.Itoa(lo) + ":" + strconv.Itoa(hi)
		_, se, err := drive(t, "-per-module-emit", strconv.Itoa(idx), "-func-range", rng, "-cache-dir", cacheDir)
		if err == nil {
			plan.WriteString(strconv.Itoa(idx) + " " + strconv.Itoa(lo) + " " + strconv.Itoa(hi) + "\n")
			return
		}
		if !isOOM(err) {
			t.Fatalf("emit module %d [%d,%d) failed (not OOM): %v\n%s", idx, lo, hi, err, se)
		}
		if hi-lo <= 1 {
			t.Fatalf("emit module %d single-func window [%d,%d) still OOMed; sharding cannot help", idx, lo, hi)
		}
		mid := lo + (hi-lo)/2
		t.Logf("module %d window [%d,%d) OOMed; splitting at %d", idx, lo, hi, mid)
		emitWindow(idx, lo, mid)
		emitWindow(idx, mid, hi)
	}
	for i := 0; i < n; i++ {
		if counts[i] <= window {
			emitWindow(i, 0, counts[i])
			continue
		}
		for lo := 0; lo < counts[i]; lo += window {
			hi := lo + window
			if hi > counts[i] {
				hi = counts[i]
			}
			emitWindow(i, lo, hi)
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
	t.Logf("whole-compiler wasm link OK: %d modules, %d bytes WAT, validated", n, len(wat))
}
