package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// Storage order must not force a jump across a ready successor. A preferred
// successor must still wait for all predecessors and stay in its loop region.
func TestSelfHostSSALayoutReadySuccessor(t *testing.T) {
	const src = `import "./ssa";
import "./ssalayout";
function br(target: i32): ssa.STerm {
  return ssa.STerm { kind_tag: 2, cond: 0, target: target, t: 0, f: 0, value: 0 };
}
function branch(t: i32, f: i32): ssa.STerm {
  return ssa.STerm { kind_tag: 3, cond: 0, target: 0, t: t, f: f, value: 0 };
}
function ret(): ssa.STerm {
  return ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: 0 };
}
function block(id: i32, preds: i32[], term: ssa.STerm): ssa.SBlock {
  let insts: ssa.SInst[] = [];
  if (id == 0) { insts = [ssa.SInst { kind_tag: 6, result: 0, args: [], imm: 0, str: "" }]; }
  return ssa.SBlock { id: id, preds: preds, insts: insts, term: term };
}
function graph(blocks: ssa.SBlock[]): ssa.SFunc {
  return ssa.SFunc { name: "layout", nparams: 1, nvals: 1, entry: 0, takes_env: false, blocks: blocks };
}
function matches(g: ssa.SFunc, want: i32[], ends: i32[]): boolean {
  let layout = ssalayout.compute(g);
  if (!layout.ok || layout.order.len() != want.len() || layout.loop_end.len() != ends.len()) { return false; }
  let i: i32 = 0;
  while (i < want.len()) {
    if (g.blocks[layout.order[i]].id != want[i] || layout.loop_end[i] != ends[i]) { return false; }
    i = i + 1;
  }
  return true;
}
function main(): i32 {
  let diamond = graph([block(0, [], branch(2, 1)), block(1, [0], br(3)),
    block(2, [0], br(3)), block(3, [1, 2], ret())]);
  if (!matches(diamond, [0, 2, 1, 3], [-1, -1, -1, -1])) { return 1; }
  // The preferred true successor is a join with a still-pending predecessor.
  let pending = graph([block(0, [], branch(3, 1)), block(1, [0], br(2)),
    block(2, [1], br(3)), block(3, [0, 2], ret())]);
  if (!matches(pending, [0, 1, 2, 3], [-1, -1, -1, -1])) { return 2; }
  // Reverse storage order, with both an inner break and an outer back edge.
  let nested = graph([block(0, [], br(1)), block(7, [1], ret()), block(6, [5], br(1)),
    block(5, [2, 3], br(6)), block(4, [3], br(2)), block(3, [2], branch(4, 5)),
    block(2, [1, 4], branch(3, 5)), block(1, [0, 6], branch(2, 7))]);
  if (!matches(nested, [0, 1, 2, 3, 4, 5, 6, 7], [-1, 6, 4, -1, -1, -1, -1, -1])) { return 3; }
  let irreducible = graph([block(0, [], branch(1, 2)), block(1, [0, 2], br(2)),
    block(2, [0, 1], branch(1, 3)), block(3, [2], ret())]);
  if (ssalayout.compute(irreducible).ok) { return 4; }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssalayout.fern")
	if err := os.WriteFile(filepath.Join(dir, "layout-successor.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "layout-successor.fern", "layout-successor")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("ready successor layout: %v\n%s", err, out)
	}
}
