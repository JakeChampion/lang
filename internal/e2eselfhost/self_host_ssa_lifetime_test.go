package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
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
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := selfHostLifetimeFixtures()
	for _, name := range []string{"nested-projection", "phi-edges", "loop-anchor", "rebound-value"} {
		t.Run(name, func(t *testing.T) {
			source, want := lifetimeFernFixture(t, fixtures[name])
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
				run = exec.Command(wasmtime, "run", wat)
			}
			got, err := run.CombinedOutput()
			if err != nil || string(got) != want {
				t.Fatalf("self-host lifetime solver: %v\ngot %s\nwant %s", err, got, want)
			}
		})
	}
}

type lifetimeFixture struct {
	f    *ssa.Func
	deps map[int32][]ssa.Value
}

func selfHostLifetimeFixtures() map[string]lifetimeFixture {
	out := map[string]lifetimeFixture{}
	{
		f := ssa.NewFunc("nested-projection")
		entry, mid, exit := f.NewBlock(), f.NewBlock(), f.NewBlock()
		root := f.AddOp(entry, ssa.OpConstInt)
		child := f.AddOp(entry, ssa.OpAdd, root, root)
		leaf := f.AddOp(entry, ssa.OpAdd, child, child)
		f.SetBr(entry, mid)
		f.SetBr(mid, exit)
		f.SetRet(exit, leaf)
		out["nested-projection"] = lifetimeFixture{f, map[int32][]ssa.Value{child.ID: {root}, leaf.ID: {child, root}}}
	}
	{
		f := ssa.NewFunc("phi-edges")
		entry, left, right, join := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
		cond := f.AddOp(entry, ssa.OpConstInt)
		f.SetBrIf(entry, cond, left, right)
		lr := f.AddOp(left, ssa.OpConstInt)
		lc := f.AddOp(left, ssa.OpAdd, lr, lr)
		f.SetBr(left, join)
		rr := f.AddOp(right, ssa.OpConstInt)
		rc := f.AddOp(right, ssa.OpAdd, rr, rr)
		f.SetBr(right, join)
		phi := f.AddPhi(join, lc, rc)
		f.SetRet(join, phi)
		out["phi-edges"] = lifetimeFixture{f, map[int32][]ssa.Value{lc.ID: {lr}, rc.ID: {rr}}}
	}
	{
		f := ssa.NewFunc("loop-anchor")
		entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
		root := f.AddOp(entry, ssa.OpConstInt)
		child := f.AddOp(entry, ssa.OpAdd, root, root)
		f.SetBr(entry, header)
		phi := f.AddPhi(header, child, child)
		cond := f.AddOp(header, ssa.OpConstInt)
		f.SetBrIf(header, cond, body, exit)
		f.AddOp(body, ssa.OpAdd, phi, child)
		f.SetBr(body, header)
		f.SetRet(exit, phi)
		// Reverse storage order: dataflow must use CFG edges, not source order.
		f.Blocks = []*ssa.Block{exit, body, header, entry}
		out["loop-anchor"] = lifetimeFixture{f, map[int32][]ssa.Value{child.ID: {root}}}
	}
	{
		f := ssa.NewFunc("rebound-value")
		entry, oldExit, exit := f.NewBlock(), f.NewBlock(), f.NewBlock()
		oldValue := f.AddOp(entry, ssa.OpConstInt)
		actualRoot := f.AddOp(entry, ssa.OpConstInt)
		child := f.AddOp(entry, ssa.OpAdd, actualRoot, actualRoot)
		f.SetBrIf(entry, f.AddOp(entry, ssa.OpConstInt), oldExit, exit)
		f.SetRet(oldExit, oldValue)
		f.SetRet(exit, child)
		out["rebound-value"] = lifetimeFixture{f, map[int32][]ssa.Value{child.ID: {actualRoot}}}
	}
	return out
}

// Serialize the same graph and dependencies for the native SSA oracle and
// the executable Fern implementation. This tests the port's dataflow, not an
// AST-to-SSA reconstruction or physical RC instruction recognition.
func lifetimeFernFixture(t *testing.T, tc lifetimeFixture) (string, string) {
	t.Helper()
	if err := ssa.Verify(tc.f); err != nil {
		t.Fatalf("invalid oracle graph: %v", err)
	}
	nvals := int32(0)
	for _, b := range tc.f.Blocks {
		for _, op := range b.Ops {
			if op.Result.ID > nvals {
				nvals = op.Result.ID
			}
		}
	}
	ints := func(vs []ssa.Value) string {
		var out []string
		for _, v := range vs {
			// Native ids start at one; the Fern graph uses value zero too.
			out = append(out, fmt.Sprint(v.ID-1))
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	blockID := func(b *ssa.Block) int32 { return b.ID*10 + 7 }
	var source strings.Builder
	source.WriteString("import \"./ssa\";\nimport \"./ssalive\";\nfunction main(): i32 {\nvar blocks: ssa.SBlock[] = [];\n")
	for _, b := range tc.f.Blocks {
		var insts, preds []string
		for _, op := range b.Ops {
			kind := 9
			if op.Kind == ssa.OpPhi {
				kind = 8
			} else if len(op.Args) == 0 {
				kind = 1
			}
			insts = append(insts, fmt.Sprintf("ssa.SInst { kind_tag: %d, result: %d, args: %s, imm: 0, str: \"\" }", kind, op.Result.ID-1, ints(op.Args)))
		}
		for _, p := range b.Preds {
			preds = append(preds, fmt.Sprint(blockID(p)))
		}
		kind, cond, target, yes, no, value := 1, int32(0), int32(0), int32(0), int32(0), b.Term.Value.ID-1
		switch b.Term.Kind {
		case ssa.TermBr:
			kind, target = 2, blockID(b.Term.Target)
		case ssa.TermBrIf:
			kind, cond, yes, no = 3, b.Term.Cond.ID-1, blockID(b.Term.True), blockID(b.Term.False)
		}
		fmt.Fprintf(&source, "blocks = blocks.append(ssa.SBlock { id: %d, insts: [%s], preds: [%s], term: ssa.STerm { kind_tag: %d, cond: %d, target: %d, t: %d, f: %d, value: %d } });\n", blockID(b), strings.Join(insts, ","), strings.Join(preds, ","), kind, cond, target, yes, no, value)
	}
	var deps []string
	for v := int32(0); v < nvals; v++ {
		deps = append(deps, ints(tc.deps[v+1]))
	}
	fmt.Fprintf(&source, "var f = ssa.SFunc { name: \"fixture\", nparams: 0, nvals: %d, blocks: blocks, entry: %d, takes_env: false };\nvar deps: i32[][] = [%s];\n", nvals, blockID(tc.f.Entry), strings.Join(deps, ","))
	source.WriteString(`var before = ssa.print_func(f);
var result = ssalive.compute(f, deps);
if (!result.ok) { print(result.why); return 1; }
if (ssa.print_func(f) != before) { return 2; }
var bits: string = "";
for bit in result.live_in { if (bit) { bits = bits + "1"; } else { bits = bits + "0"; } }
print(bits);
bits = "";
for bit in result.live_out { if (bit) { bits = bits + "1"; } else { bits = bits + "0"; } }
print(bits);
return 0;
}
`)
	live := ssa.ComputeLivenessWithDependencies(tc.f, tc.deps)
	var want strings.Builder
	for _, sets := range []map[*ssa.Block]map[int32]bool{live.LiveIn, live.LiveOut} {
		for _, b := range tc.f.Blocks {
			for v := int32(0); v < nvals; v++ {
				if sets[b][v+1] {
					want.WriteByte('1')
				} else {
					want.WriteByte('0')
				}
			}
		}
		want.WriteByte('\n')
	}
	return source.String(), want.String()
}

func TestSelfHostSSALifetimeDependencies(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	fixtures := selfHostLifetimeFixtures()
	var names []string
	for name := range fixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			source, want := lifetimeFernFixture(t, fixtures[name])
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
				t.Fatalf("live sets differ from native SSA:\ngot %s\nwant %s", got, want)
			}
		})
	}
}

func TestSelfHostSSALifetimeInvalidMetadata(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	source, _ := lifetimeFernFixture(t, selfHostLifetimeFixtures()["nested-projection"])
	for _, tc := range []struct{ name, change, want string }{
		{"dimensions", "deps = [];", "dependency dimensions"},
		{"dependency-id", "deps = deps.with(1, [f.nvals]);", "dependency value out of range"},
		{"entry", "f = ssa.SFunc { ...f, entry: 0 - 1 };", "missing entry block"},
		{"predecessor", "f = ssa.SFunc { ...f, blocks: f.blocks.with(1, ssa.SBlock { ...f.blocks[1], preds: [] }) };", "missing predecessor edge"},
		{"terminator", "var b = f.blocks[0]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, kind_tag: 0 } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "invalid terminator"},
		{"negative-successor", "var b = f.blocks[0]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, target: 0 - 1 } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "negative successor"},
		{"missing-successor", "var b = f.blocks[0]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, target: 999 } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "missing successor"},
		{"phi-arity", "var b = f.blocks[0]; var ins = ssa.SInst { ...b.insts[2], kind_tag: 8 }; b = ssa.SBlock { ...b, insts: b.insts.with(2, ins) }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "phi predecessor arity"},
		{"operand-id", "var b = f.blocks[0]; var ins = ssa.SInst { ...b.insts[2], args: [f.nvals] }; b = ssa.SBlock { ...b, insts: b.insts.with(2, ins) }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "operand value out of range"},
		{"result-id", "var b = f.blocks[0]; var ins = ssa.SInst { ...b.insts[2], result: f.nvals }; b = ssa.SBlock { ...b, insts: b.insts.with(2, ins) }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "result value out of range"},
		{"return-id", "var b = f.blocks[2]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, value: f.nvals } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(2, b) };", "return value out of range"},
		{"condition-id", "var b = f.blocks[0]; b = ssa.SBlock { ...b, term: ssa.STerm { ...b.term, kind_tag: 3, cond: f.nvals } }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "condition value out of range"},
		{"duplicate-block", "f = ssa.SFunc { ...f, blocks: [f.blocks[0], f.blocks[1], f.blocks[2], f.blocks[0]] };", "duplicate or negative block id"},
		{"negative-block", "f = ssa.SFunc { ...f, blocks: [ssa.SBlock { ...f.blocks[2], id: 0 - 1 }, f.blocks[0], f.blocks[1], f.blocks[2]] };", "duplicate or negative block id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(source, "var before =", tc.change+"\nvar before =", 1)
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
