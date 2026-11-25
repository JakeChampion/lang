package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// ssa.merge_duplicates merges a pure operation into the same operation in its
// own block. With `across` it also merges one in a block that dominates it,
// except a compare, an operation in a sibling block, and an operation that
// reads a value with two definitions.
func TestSelfHostMergeDuplicatesAcrossBlocks(t *testing.T) {
	const src = `import "./ssa";

function inst(kind: i32, result: i32, args: i32[], imm: i32, op: string): ssa.SInst {
  return ssa.SInst { kind_tag: kind, result: result, args: args, imm: imm, str: op };
}

// b0 computes v2 = v0 * v1, v3 = v0 == v1 and v6 = v5 - v1, then branches.
// b1 repeats the mul and the compare. b2 repeats the add of its sibling b1,
// and the sub of b0 with v5, which a copy defines in b0 and again in b2.
function graph(): ssa.SFunc {
  let blocks = [
    ssa.SBlock { id: 0, preds: [], insts: [
      inst(ssa.PARAM, 0, [], 0, ""),
      inst(ssa.PARAM, 1, [], 1, ""),
      inst(ssa.BINARY, 2, [0, 1], 32, "mul"),
      inst(ssa.BINARY, 3, [0, 1], 32, "eq"),
      inst(ssa.COPY, 5, [0], 0, ""),
      inst(ssa.BINARY, 6, [5, 1], 32, "sub")
    ], term: ssa.t_brif(3, 1, 2) },
    ssa.SBlock { id: 1, preds: [0], insts: [
      inst(ssa.BINARY, 7, [0, 1], 32, "mul"),
      inst(ssa.BINARY, 8, [0, 1], 32, "eq"),
      inst(ssa.BINARY, 9, [0, 1], 32, "add"),
      inst(ssa.BINARY, 10, [7, 8], 32, "add"),
      inst(ssa.BINARY, 11, [10, 9], 32, "add")
    ], term: ssa.t_ret(11) },
    ssa.SBlock { id: 2, preds: [0], insts: [
      inst(ssa.COPY, 5, [1], 0, ""),
      inst(ssa.BINARY, 12, [5, 1], 32, "sub"),
      inst(ssa.BINARY, 13, [0, 1], 32, "add"),
      inst(ssa.BINARY, 14, [12, 13], 32, "add"),
      inst(ssa.BINARY, 15, [14, 6], 32, "add")
    ], term: ssa.t_ret(15) }
  ];
  return ssa.SFunc { name: "merge", nparams: 2, nvals: 16, blocks: blocks, entry: 0, takes_env: false };
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
  let local = ssa.merge_duplicates(graph(), false);
  if (!same(results(local.f.blocks[1].insts), [7, 8, 9, 10, 11])) {
    return 1;
  }
  let f = ssa.merge_duplicates(graph(), true).f;
  if (!same(results(f.blocks[0].insts), [0, 1, 2, 3, 5, 6])) {
    return 2;
  }
  // The mul merges into v2. The compare v8 and the add v9 stay.
  if (!same(results(f.blocks[1].insts), [8, 9, 10, 11]) || !same(f.blocks[1].insts[2].args, [2, 8])) {
    return 3;
  }
  // The sibling add v13 stays, and so does v12, which reads the second v5.
  if (!same(results(f.blocks[2].insts), [5, 12, 13, 14, 15])) {
    return 4;
  }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa.fern")
	if err := os.WriteFile(filepath.Join(dir, "merge-duplicates.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "merge-duplicates.fern", "merge-duplicates")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("merge duplicates: %v\n%s", err, out)
	}
}
