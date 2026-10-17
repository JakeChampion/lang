package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostByteCopySparseBlockIDs(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := fullSelfHostProject(t)
	if err := os.WriteFile(filepath.Join(dir, "byte_copy_sparse.fern"), []byte(byteCopySparseDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "byte_copy_sparse.fern", "byte_copy_sparse")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("byte loop with non-contiguous block identities: %v\n%s", err, out)
	}
}

const byteCopySparseDriver = `import "./semsource";
import "./sembytecopy";
import "./ssa";
import "./ssasem";
import "./ssaunits";
import "./typeinfo";
import "./parser";
import "./lexer";

function ins(kind: i32, result: i32, args: i32[], imm: i32, text: string): ssa.SInst {
  return ssa.SInst { kind_tag: kind, result: result, args: args, imm: imm, str: text };
}
function term(kind: i32, target: i32): ssa.STerm {
  return ssa.STerm { kind_tag: kind, target: target, t: 0, f: 0, cond: 0, value: 0 };
}
function main(): i32 {
  let mod = parser.parse_module(lexer.tokenize("function seed(own a: u8[], src: u8[], n: i32): u8[] { return a; }"));
  let f = semsource.build_module(mod).decls[0].func;
  let a = f.params[0]; let n = f.params[2];
  let b: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
  let u: typeinfo.Type = typeinfo.TypeI32 { width: 8, unsigned: true, is_char: false, polymorphic: false };
  let values = [a, a, n, n, a, n, b, u, a, n, n];
  let blocks = [
    ssa.SBlock { id: 100, preds: [], insts: [ins(6,0,[],0,""), ins(6,1,[],1,""), ins(6,2,[],2,""), ins(1,3,[],0,"")], term: term(2,500) },
    ssa.SBlock { id: 500, preds: [100,700], insts: [ins(8,4,[0,8],0,""), ins(8,5,[3,10],0,""), ins(9,6,[5,2],0,"<")], term: ssa.STerm { ...term(3,0), cond: 6, t: 700, f: 900 } },
    ssa.SBlock { id: 700, preds: [500], insts: [ins(ssasem.array_get(),7,[1,5],0,""), ins(ssasem.append(),8,[4,7],0,""), ins(1,9,[],1,""), ins(9,10,[5,9],0,"+")], term: term(2,500) },
    ssa.SBlock { id: 900, preds: [500], insts: [], term: ssa.STerm { ...term(1,0), value: 4 } },
  ];
  f = ssasem.Func { ...f, values: values, graph: ssa.SFunc { ...f.graph, blocks: blocks, entry: 100, nvals: values.len() } };
  let contracts = [
    ssasem.Contract { name: "__fern_bytes_append_range", params: [a,a,n,n], modes: [3,2,1,1], result: a },
    ssasem.Contract { name: "__fern_bytes_write_range", params: [a,n,a,n,n], modes: [3,1,2,1,1], result: a },
  ];
  let r = sembytecopy.rewrite(f, contracts);
  if (!r.changed) { return 1; }
  let analysis = ssasem.analyze(r.func);
  if (!analysis.ok) { eprint(analysis.why); return 2; }
  let plan = ssaunits.plan(r.func, [3,2,1], ssaunits.no_view());
  if (!plan.ok) { eprint(plan.why); return 3; }
  return 0;
}
`
