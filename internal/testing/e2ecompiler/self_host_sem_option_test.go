package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// Exercise the typed pass directly, including boundaries not reachable from
// array.reduce today. Both its input and output must pass the semantic verifier.
func TestSelfHostSemanticOptionAdmission(t *testing.T) {
	const src = `import "./ssa";
import "./ssasem";
import "./semrecords";
import "./semoption";
import "./typeinfo";
function fixture(row: i32): ssasem.Func {
  let word: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false };
  let flag: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
  let payload = word;
  if (row == 6) { payload = typeinfo.TypeString { tag: 0 }; }
  let whole: typeinfo.Type = typeinfo.TypeUnion { name: "Option", args: [payload] };
  let variants = [semrecords.Variant { name: "Some", fields: [semrecords.Field { name: "value", ty: payload }] },
    semrecords.Variant { name: "None", fields: [] }];
  let layout = semrecords.layout_option();
  if (row == 8) { layout = semrecords.layout_variant(); }
  let enums = [semrecords.Enum { ty: whole, variants: variants, layout: layout, views: false, nests_func: false }];
  let constant = ssa.SInst { kind_tag: 1, result: 0, args: [], imm: 7, str: "" };
  if (row == 6) { constant = ssa.SInst { ...constant, kind_tag: 5, str: "payload" }; }
  let fallback = constant;
  let cons = ssa.SInst { kind_tag: ssasem.variant_new(), result: 1, args: [0], imm: 0, str: "Some" };
  let params: typeinfo.Type[] = [];
  if (row == 7) {
    constant = ssa.SInst { ...constant, kind_tag: ssasem.param(), imm: 0 };
    cons = ssa.SInst { ...cons, kind_tag: ssasem.param(), args: [], imm: 1, str: "" };
    params = [payload, whole];
  }
  let insts = [constant, cons];
  let values = [payload, whole, flag, payload, payload];
  let calls: ssasem.Contract[] = [];
  if (row == 1) {
    insts = insts.append(ssa.SInst { kind_tag: ssasem.call(), result: 5, args: [1], imm: 0, str: "observe" });
    values = values.append(word);
    calls = [ssasem.Contract { name: "observe", params: [whole], modes: [0], result: word }];
  }
  if (row == 2) {
    insts = insts.append(ssa.SInst { kind_tag: ssasem.array_new(), result: 5, args: [1], imm: 0, str: "" });
    values = values.append(typeinfo.TypeArray { elem: whole, view: false });
  }
  insts = insts.append(ssa.SInst { kind_tag: ssasem.variant_is(), result: 2, args: [1], imm: 0, str: "Some" });
  let blocks = [ssa.SBlock { id: 0, preds: [], insts: insts,
    term: ssa.STerm { kind_tag: 3, cond: 2, target: 0, t: 1, f: 2, value: 0 } },
    ssa.SBlock { id: 1, preds: [0], insts: [ssa.SInst { kind_tag: ssasem.variant_get(), result: 3, args: [1], imm: 0, str: "Some" }],
      term: ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: 3 } },
    ssa.SBlock { id: 2, preds: [0], insts: [ssa.SInst { ...fallback, result: 4 }],
      term: ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: 4 } }];
  let result = payload;
  if (row == 3) {
    result = whole;
    blocks = [ssa.SBlock { ...blocks[0], term: ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: 1 } }];
  }
  let debug: i32[] = [];
  let names: string[] = [];
  if (row == 4) { debug = [1]; names = ["out"]; }
  let finalizers: string[] = [];
  if (row == 5) { finalizers = ["Option"]; }
  return ssasem.Func { graph: ssa.SFunc { name: "option", nparams: params.len(), nvals: values.len(), entry: 0, takes_env: false, blocks: blocks },
    values: values, params: params, result: result, records: semrecords.no_records(), enums: enums,
    calls: calls, envs: [], anchors: [], dyns: [], shadows: [], finalizers: finalizers,
    map_module: false, dbg_vals: debug, dbg_names: names };
}
function main(): i32 {
  let row: i32 = 0;
  while (row < 9) {
    let before = fixture(row);
    let checked = ssasem.analyze(before);
    if (!checked.ok) { print(checked.why); return row + 1; }
    let after = semoption.split(before);
    checked = ssasem.analyze(after);
    if (!checked.ok) { print(checked.why); return row + 11; }
    if ((after.graph.nvals > before.graph.nvals) != (row == 0)) { return row + 21; }
    row = row + 1;
  }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "semoption.fern")
	if err := os.WriteFile(filepath.Join(dir, "option-admission.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "option-admission.fern", "option-admission")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("option admission: %v\n%s", err, out)
	}
}
