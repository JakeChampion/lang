package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostSSALifetimeIRArm64(t *testing.T)  { testSelfHostSSALifetimeIR(t, "arm64-linux") }
func TestSelfHostSSALifetimeIRX86_64(t *testing.T) { testSelfHostSSALifetimeIR(t, "x86-64-linux") }
func TestSelfHostSSALifetimeIRWasm(t *testing.T)   { testSelfHostSSALifetimeIR(t, "wasm32-wasi") }

func testSelfHostSSALifetimeIR(t *testing.T, target string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	var armGCC, armRunner, wasmtime string
	if target == "arm64-linux" {
		armGCC, armRunner = arm64Tooling(t)
	}
	if target == "wasm32-wasi" {
		var err error
		wasmtime, err = exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH")
		}
	}
	dir := copySelfHostTree(t)
	copySelfHostDriver(t, dir, "ssalive.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range lifetimeFixtureNames {
		t.Run(name, func(t *testing.T) {
			source, want := lifetimeFixture(t, name)
			entry := filepath.Join(dir, "lifetime_fixture.fern")
			if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(runner, driver, "-target", target, "-emit", "asm", entry, root)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			output, err := cmd.Output()
			if err != nil || len(output) == 0 {
				t.Fatalf("self-host compile: %v\n%s", err, diagnostics.String())
			}
			var run *exec.Cmd
			switch target {
			case "x86-64-linux":
				run = runX86_64Bin(runner, buildBin(t, gcc, dir, name, string(output)))
			case "arm64-linux":
				run = runArm64Bin(armRunner, buildBinArm64(t, armGCC, dir, name, string(output)))
			case "wasm32-wasi":
				wat := filepath.Join(dir, name+".wat")
				if err := os.WriteFile(wat, output, 0o644); err != nil {
					t.Fatal(err)
				}
				// wasmtime hands the guest no host variable it is not told to.
				run = exec.Command(wasmtime, "run", "--env", e2eharness.SelfHostVerify, wat)
			}
			got, err := run.CombinedOutput()
			if err != nil || string(got) != want {
				t.Fatalf("self-host lifetime solver: %v\ngot %s\nwant %s", err, got, want)
			}
		})
	}
}

// lifetimeFixtureNames are the dataflow graphs under testdata/lifetime. Each
// <name>.fern builds an ssa.SFunc by hand, runs ssalive.compute over it with
// the per-value dependency lists, and prints the live-in and live-out bit rows
// (one bit per block per value) after checking the id-based accessors agree
// with the bit sets; <name>.want is the two rows the Go SSA liveness solver
// produced for the same graph, recorded when that package was deleted. The
// graphs: a projection chain through two blocks, a diamond whose phi takes a
// child from each arm, a loop whose header phi carries a child (stored in
// reverse order, so dataflow must use CFG edges, not source order), a value
// rebound before a branch, and a graph wide enough that each live set spans
// several 64-bit words.
var lifetimeFixtureNames = []string{"nested-projection", "phi-edges", "loop-anchor", "rebound-value", "wide"}

func lifetimeFixture(t *testing.T, name string) (source, want string) {
	t.Helper()
	read := func(ext string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "lifetime", name+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return read(".fern"), read(".want")
}

func TestSelfHostSSALifetimeDependencies(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	for _, name := range lifetimeFixtureNames {
		t.Run(name, func(t *testing.T) {
			source, want := lifetimeFixture(t, name)
			dir := t.TempDir()
			copySelfHostDriver(t, dir, "ssalive.fern")
			if err := os.WriteFile(filepath.Join(dir, "lifetime_fixture.fern"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := buildSelfHostBin(t, gcc, dir, "lifetime_fixture.fern", "lifetime")
			argv := append(append([]string{}, runner...), bin)
			cmd := exec.Command(argv[0], argv[1:]...)
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Fern lifetime solver: %v\n%s", err, got)
			}
			if string(got) != want {
				t.Fatalf("live sets differ from the recorded solution:\ngot %s\nwant %s", got, want)
			}
		})
	}
}

func TestSelfHostSSALifetimeInvalidMetadata(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	source, _ := lifetimeFixture(t, "nested-projection")
	for _, tc := range []struct{ name, change, want string }{
		{"dimensions", "deps = [];", "dependency dimensions"},
		{"dependency-id", "deps = deps.with(1, [f.nvals]);", "dependency value out of range"},
		{"entry", "f = ssa.SFunc { ...f, entry: 0 - 1 };", "missing entry block"},
		{"predecessor", "f = ssa.SFunc { ...f, blocks: f.blocks.with(1, ssa.SBlock { ...f.blocks[1], preds: [] }) };", "missing predecessor edge"},
		{"terminator", "let b = f.blocks[0]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, kind_tag: 0 } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "invalid terminator"},
		{"negative-successor", "let b = f.blocks[0]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, target: 0 - 1 } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "negative successor"},
		{"missing-successor", "let b = f.blocks[0]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, target: 999 } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "missing successor"},
		{"phi-arity", "let b = f.blocks[0]; let ins = ssa.SInst { ...b.insts[2], kind_tag: 8 }; b = ssa.SBlock { ...b, insts: b.insts.with(2, ins) }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "phi predecessor arity"},
		{"operand-id", "let b = f.blocks[0]; let ins = ssa.SInst { ...b.insts[2], args: [f.nvals] }; b = ssa.SBlock { ...b, insts: b.insts.with(2, ins) }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "operand value out of range"},
		{"result-id", "let b = f.blocks[0]; let ins = ssa.SInst { ...b.insts[2], result: f.nvals }; b = ssa.SBlock { ...b, insts: b.insts.with(2, ins) }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "result value out of range"},
		{"return-id", "let b = f.blocks[2]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, value: f.nvals } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(2, b) };", "return value out of range"},
		{"condition-id", "let b = f.blocks[0]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, kind_tag: 3, cond: f.nvals } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "condition value out of range"},
		{"duplicate-block", "f = ssa.SFunc { ...f, blocks: [f.blocks[0], f.blocks[1], f.blocks[2], f.blocks[0]] };", "duplicate or negative block id"},
		{"negative-block", "f = ssa.SFunc { ...f, blocks: [ssa.SBlock { ...f.blocks[2], id: 0 - 1 }, f.blocks[0], f.blocks[1], f.blocks[2]] };", "duplicate or negative block id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(source, "let before =", tc.change+"\nlet before =", 1)
			dir := t.TempDir()
			copySelfHostDriver(t, dir, "ssalive.fern")
			if err := os.WriteFile(filepath.Join(dir, "lifetime_fixture.fern"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := buildSelfHostBin(t, gcc, dir, "lifetime_fixture.fern", "lifetime")
			argv := append(append([]string{}, runner...), bin)
			cmd := exec.Command(argv[0], argv[1:]...)
			got, _ := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || string(got) != tc.want+"\n" {
				t.Fatalf("invalid graph was not rejected: state=%v output=%s", cmd.ProcessState, got)
			}
		})
	}
}
