package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostLoopIndexCopy(t *testing.T) {
	const src = `import "./ssa";

function read(v: i32): ssa.SInst {
  return ssa.SInst { ir_kind: 0, kind_tag: ssa.ARR_AT, result: v, args: [0, 1], imm: 0, str: "i32" };
}

function graph(side_entry: boolean): ssa.SFunc {
  let zero = ssa.SInst { ir_kind: 0, kind_tag: ssa.PARAM, result: 0, args: [], imm: 0, str: "" };
  let one = ssa.SInst { ...zero, result: 1, imm: 1 };
  let entry = if (side_entry) { ssa.t_brif(1, 1, 4) } else { ssa.t_br(1) };
  let blocks = [
    ssa.SBlock { id: 0, preds: [], insts: [zero, one, read(2)], term: entry },
    ssa.SBlock { id: 1, preds: [0, 6], insts: [], term: ssa.t_brif(1, 2, 7) },
    ssa.SBlock { id: 2, preds: [1], insts: [], term: ssa.t_br(3) },
    ssa.SBlock { id: 3, preds: [2, 5], insts: [read(5)], term: ssa.t_brif(1, 4, 6) },
    ssa.SBlock { id: 4, preds: if (side_entry) { [3, 0] } else { [3] }, insts: [read(3)], term: ssa.t_br(5) },
    ssa.SBlock { id: 5, preds: [4], insts: [read(4)], term: ssa.t_br(3) },
    ssa.SBlock { id: 6, preds: [3], insts: [], term: ssa.t_br(1) },
    ssa.SBlock { id: 7, preds: [1], insts: [], term: ssa.t_ret(2) }
  ];
  return ssa.SFunc { name: "index", nparams: 2, nvals: 6, blocks: blocks, entry: 0, takes_env: false };
}

function main(): i32 {
  let slots = [-2, 0, 1, 2, 3, 4];
  let f = ssa.loop_index_copy(graph(false), slots);
  if (f.nvals != 7 || f.blocks[2].insts.len() != 1) { return 1; }
  let copy = f.blocks[2].insts[0];
  if (copy.kind_tag != ssa.COPY || copy.result != 6 || copy.args[0] != 0) { return 2; }
  if (f.blocks[0].insts[2].args[0] != 0 || f.blocks[3].insts[0].args[0] != 6 || f.blocks[4].insts[0].args[0] != 6 || f.blocks[5].insts[0].args[0] != 6) { return 3; }
  if (ssa.loop_index_copy(graph(true), slots).nvals != 6) { return 4; }
  if (ssa.loop_index_copy(graph(false), [0, 1, 2, 3, 4, 5]).nvals != 6) { return 5; }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa.fern")
	if err := os.WriteFile(filepath.Join(dir, "loop-index.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "loop-index.fern", "loop-index")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("loop index copy: %v\n%s", err, out)
	}
}
