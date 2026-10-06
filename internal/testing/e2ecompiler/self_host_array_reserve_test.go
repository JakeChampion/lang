package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostArrayReserveAdmission(t *testing.T) {
	const src = `import "./ssa";
import "./ssasem";
import "./semrecords";
import "./typeinfo";
function main(): i32 {
  let word: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false };
  let row: i32 = 0;
  while (row < 7) {
    let elem = word;
    if (row == 1) { elem = typeinfo.TypeFloat { width: 64, polymorphic: false }; }
    if (row == 5) { elem = typeinfo.TypeString { tag: 0 }; }
    let result: typeinfo.Type = typeinfo.TypeArray { elem: elem, view: row == 4 };
    if (row == 6) { result = word; }
    let capacity = word;
    let kind: i32 = 1;
    if (row == 2) { capacity = typeinfo.TypeBool { tag: 0 }; kind = 2; }
    let args = [0];
    if (row == 3) { args = []; }
    let ops = [ssa.SInst { kind_tag: kind, result: 0, args: [], imm: 0, str: "" },
      ssa.SInst { kind_tag: ssasem.array_reserve(), result: 1, args: args, imm: 0, str: "" }];
    let block = ssa.SBlock { id: 0, preds: [], insts: ops,
      term: ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: 1 } };
    let graph = ssa.SFunc { name: "reserve", nparams: 0, nvals: 2, entry: 0, takes_env: false, blocks: [block] };
    let f = ssasem.Func { graph: graph, values: [capacity, result], params: [], result: result,
      records: semrecords.no_records(), enums: [], calls: [], envs: [], anchors: [], dyns: [],
      shadows: [], finalizers: [], map_module: false, dbg_vals: [], dbg_names: [] };
    let checked = ssasem.analyze(f);
    if (checked.ok != (row < 2)) { print(checked.why); return row + 1; }
    row = row + 1;
  }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssasem.fern")
	if err := os.WriteFile(filepath.Join(dir, "reserve-admission.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "reserve-admission.fern", "reserve-admission")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("reserve admission: %v\n%s", err, out)
	}
}
