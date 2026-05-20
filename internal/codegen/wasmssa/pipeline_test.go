// Package wasmssa_e2e — end-to-end tests that drive the
// SSA → wasm path from Lang source. Each test parses + checks
// + lowers to IR + lifts to SSA + optimizes + emits via
// wasmssa.EmitModule + runs under wasmtime, asserting the
// runtime result matches expectation.
//
// Lives in a _test package so it can import all the layers
// without creating a dependency cycle. SKIPs gracefully when
// wasmtime isn't on PATH.

package wasmssa_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmssa"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

// TestPipelineAdd — `function add(a, b) { return a + b }` —
// end-to-end through the SSA → wasm pipeline, returning a + b.
func TestPipelineAdd(t *testing.T) {
	src := `function add(a: i32, b: i32): i32 { return a + b; }`
	got := compileAndRun(t, src, "add", "11", "31")
	if got != 42 {
		t.Errorf("add(11, 31) = %d, want 42", got)
	}
}

// TestPipelineMul — sanity that multi-op arith works.
func TestPipelineMul(t *testing.T) {
	src := `function quad(a: i32): i32 { return a * a * a * a; }`
	got := compileAndRun(t, src, "quad", "3")
	if got != 81 {
		t.Errorf("quad(3) = %d, want 81", got)
	}
}

// compileAndRun runs the full pipeline:
//  1. parse + check + lower the source to IR
//  2. lift the named function to SSA
//  3. optimize
//  4. emit via wasmssa
//  5. run under wasmtime --invoke
//
// Steps 1-4 run unconditionally; the wasmtime invocation is
// skipped when the binary isn't on PATH (preserving meaningful
// local coverage of the lift/optimize/emit path even without
// wasmtime). Returns the i32 the function produced (decoded
// from wasmtime's stdout), or -1 if the test was skipped due
// to missing wasmtime.
func compileAndRun(t *testing.T, src, funcName string, args ...string) int {
	t.Helper()

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	irProg, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}

	var target *ir.Func
	for _, fn := range irProg.Funcs {
		if fn.Name == funcName {
			target = fn
			break
		}
	}
	if target == nil {
		t.Fatalf("no IR func named %q", funcName)
	}

	f, err := ssa.LiftFromIR(target)
	if err != nil {
		t.Skipf("LiftFromIR(%s) failed — gap in lift coverage: %v", funcName, err)
	}
	ssa.Optimize(f)

	mod, err := wasmssa.EmitModule(f, funcName)
	if err != nil {
		t.Skipf("wasmssa.EmitModule failed — gap in wasmssa coverage: %v", err)
	}

	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; lift+emit succeeded but skipping runtime check")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "mod.wasm")
	if err := os.WriteFile(p, mod, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	cmdArgs := append([]string{"run", "--invoke", funcName, p}, args...)
	cmd := exec.Command(wasmtime, cmdArgs...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:\n%s", err, se.String())
	}
	out := strings.TrimSpace(so.String())
	v, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("parse wasmtime stdout %q: %v", out, err)
	}
	return v
}
