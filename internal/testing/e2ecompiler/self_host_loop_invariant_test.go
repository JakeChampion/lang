package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// ssa.hoisted_invariants moves a pure computation of values that do not
// change in a loop to the block before the loop, with the constants that it
// reads. A division by 0 or by a value that is not a constant, a load, and a
// loop with a second way in stay where they are.
func TestSelfHostLoopInvariantHoist(t *testing.T) {
	const src = `import "./ssa";

function inst(kind: i32, result: i32, args: i32[], imm: i32, op: string): ssa.SInst {
  return ssa.SInst { ir_kind: 0, kind_tag: kind, result: result, args: args, imm: imm, str: op };
}

function graph(side_entry: boolean): ssa.SFunc {
  let entry = if (side_entry) { ssa.t_brif(1, 1, 2) } else { ssa.t_br(1) };
  let body = [
    inst(ssa.CONST_INT, 3, [], 8, ""),
    inst(ssa.BINARY, 4, [0, 3], 32, "div_s"),
    inst(ssa.BINARY, 5, [4, 2], 32, "add"),
    inst(ssa.CONST_INT, 6, [], 0, ""),
    inst(ssa.BINARY, 8, [0, 6], 32, "div_s"),
    inst(ssa.BINARY, 9, [0, 1], 32, "div_s"),
    inst(ssa.LD_AT, 10, [0], 0, ""),
    inst(ssa.UNARY, 11, [4], 0, "int_extend"),
    inst(ssa.BINARY, 12, [11, 1], 64, "mul"),
    inst(ssa.BINARY, 7, [5, 8], 32, "add")
  ];
  let blocks = [
    ssa.SBlock { id: 0, preds: [], insts: [inst(ssa.PARAM, 0, [], 0, ""), inst(ssa.PARAM, 1, [], 1, "")], term: entry },
    ssa.SBlock { id: 1, preds: [0, 2], insts: [inst(ssa.PHI, 2, [1, 7], 0, "")], term: ssa.t_brif(2, 2, 3) },
    ssa.SBlock { id: 2, preds: if (side_entry) { [1, 0] } else { [1] }, insts: body, term: ssa.t_br(1) },
    ssa.SBlock { id: 3, preds: [1], insts: [], term: ssa.t_ret(2) }
  ];
  return ssa.SFunc { name: "invariant", nparams: 2, nvals: 13, blocks: blocks, entry: 0, takes_env: false };
}

function results(insts: ssa.SInst[]): i32[] {
  return insts.map((i: ssa.SInst): i32 => i.result);
}

function same(a: i32[], b: i32[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  for i in 0..a.len() {
    if (a[i] != b[i]) {
      return false;
    }
  }
  return true;
}

function main(): i32 {
  let f = ssa.hoisted_invariants(graph(false));
  if (!same(results(f.blocks[0].insts), [0, 1, 3, 4, 11, 12])) {
    return 1;
  }
  if (!same(results(f.blocks[2].insts), [5, 6, 8, 9, 10, 7])) {
    return 2;
  }
  if (f.nvals != 13 || f.blocks[0].term.kind_tag != ssa.TERM_BR) {
    return 3;
  }
  let side = ssa.hoisted_invariants(graph(true));
  if (side.blocks[0].insts.len() != 2 || side.blocks[2].insts.len() != 10) {
    return 4;
  }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa.fern")
	if err := os.WriteFile(filepath.Join(dir, "loop-invariant.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "loop-invariant.fern", "loop-invariant")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("loop invariant hoist: %v\n%s", err, out)
	}
}
