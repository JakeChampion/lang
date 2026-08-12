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

// TestSelfHostWasmFuncRangeShard proves function-window sharding (#5508): a
// single module emitted as TWO [lo,hi) windows links into a module that runs
// identically to the same module emitted whole.
//
// This is the mechanism the whole compiler needs for irlower (894 funcs), which
// exhausts the bump arena emitted in one process. The windows here are tiny and
// artificial; the point is only that the split-then-link path is correct, since
// scale is just more windows.
//
// The plan file drives -link: the orchestrator that decided the split tells the
// linker exactly which (idx, lo, hi) units to assemble — the wasm analogue of an
// asm build handing gcc its .s list.
func TestSelfHostWasmFuncRangeShard(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm func-range shard e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm func-range shard e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern",
		"flatten.fern", "modloader.fern", "fern_toml.fern", "wasm_objfile.fern",
		"wasm_modload_run.fern",
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

	// A single-module program with several functions to split across windows,
	// including cross-window calls and a string literal on each side of the cut.
	proj := t.TempDir()
	cacheDir := filepath.Join(proj, "cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	src := `function a(): i32 { return b() + first().len(); }
function b(): i32 { return c() * 2; }
function c(): i32 { return 4; }
function d(): i32 { return a() + second().len(); }
function first(): string { return "alpha"; }
function second(): string { return "bravo-x"; }
function main(): i32 { return d(); }`
	// c=4, b=8, a=8+5=13, d=13+7=20
	const want = 20
	entryPath := filepath.Join(proj, "entry.fern")
	if err := os.WriteFile(entryPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	drive := func(t *testing.T, args ...string) (string, string) {
		t.Helper()
		var cmd *exec.Cmd
		full := append([]string{entryPath}, args...)
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		var so, se bytes.Buffer
		cmd.Stdout = &so
		cmd.Stderr = &se
		if err := cmd.Run(); err != nil {
			t.Fatalf("driver %v failed: %v\n%s", args, err, se.String())
		}
		return so.String(), se.String()
	}

	runWat := func(t *testing.T, tag, wat string) int {
		t.Helper()
		watPath := filepath.Join(dir, tag+".wat")
		if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		corePath := filepath.Join(dir, tag+".wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("%s: wasm-tools parse: %v\n%s", tag, err, out)
		}
		out, runErr := exec.Command(wasmtime, "run", corePath).CombinedOutput()
		if runErr == nil {
			return 0
		}
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return ee.ExitCode()
		}
		t.Fatalf("%s: wasmtime run: %v\n%s", tag, runErr, out)
		return -1
	}

	// How many functions after lift/infer, so the window split is valid.
	fcOut, _ := drive(t, "-per-module-func-counts")
	fcLines := strings.Fields(strings.TrimSpace(fcOut))
	if len(fcLines) != 1 {
		t.Fatalf("-per-module-func-counts returned %d lines, want 1 (single module): %q", len(fcLines), fcOut)
	}
	nfuncs, err := strconv.Atoi(fcLines[0])
	if err != nil || nfuncs < 4 {
		t.Fatalf("func count = %q", fcOut)
	}
	mid := nfuncs / 2

	// Whole-module build (the oracle).
	whole, _ := drive(t, "-per-module-emit", "0", "-cache-dir", cacheDir)
	_ = whole
	wholeWat, _ := drive(t, "-link", "-cache-dir", cacheDir)
	if got := runWat(t, "whole", wholeWat); got != want {
		t.Fatalf("whole-module build returned %d, want %d — oracle is wrong", got, want)
	}

	// Sharded build: two windows [0,mid) and [mid,-1), emitted separately, then
	// linked via an explicit plan.
	shardCache := filepath.Join(proj, "shardcache")
	if err := os.Mkdir(shardCache, 0o755); err != nil {
		t.Fatalf("mkdir shardcache: %v", err)
	}
	if _, se := drive(t, "-per-module-emit", "0", "-func-range", "0:"+strconv.Itoa(mid), "-cache-dir", shardCache); !strings.Contains(se, "cache-miss") {
		t.Fatalf("first shard did not build: %q", se)
	}
	if _, se := drive(t, "-per-module-emit", "0", "-func-range", strconv.Itoa(mid)+":-1", "-cache-dir", shardCache); !strings.Contains(se, "cache-miss") {
		t.Fatalf("second shard did not build: %q", se)
	}
	planPath := filepath.Join(proj, "plan.txt")
	plan := "0 0 " + strconv.Itoa(mid) + "\n0 " + strconv.Itoa(mid) + " -1\n"
	if err := os.WriteFile(planPath, []byte(plan), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	shardWat, se := drive(t, "-link", "-plan", planPath, "-cache-dir", shardCache)
	if strings.Contains(se, "no cached object") {
		t.Fatalf("sharded link could not find a shard object: %q", se)
	}
	if got := runWat(t, "shard", shardWat); got != want {
		t.Errorf("two-window sharded build returned %d, want %d — sharding miscompiled", got, want)
	}

	// The sharded WAT must actually carry a shard-namespaced base, proving the
	// split happened rather than one window silently covering everything.
	if !strings.Contains(shardWat, "_s"+strconv.Itoa(mid)) {
		t.Errorf("sharded WAT has no _s%d namespace; the second window was not emitted as its own unit", mid)
	}
}
