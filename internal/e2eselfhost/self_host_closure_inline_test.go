package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// Exercise the admission proof directly as well as the compiled pipeline tests.
// Each accepted splice must still pass the typed graph verifier. Rejected
// candidates retain the call and its environment.
func TestSelfHostClosureInlineAdmission(t *testing.T) {
	const src = `import "./ssa";
import "./ssasem";
import "./seminline";
import "./semrecords";
import "./typeinfo";
function word(): typeinfo.Type { return typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false }; }
function inst(k: i32, v: i32, args: i32[], imm: i32, text: string): ssa.SInst {
  return ssa.SInst { kind_tag: k, result: v, args: args, imm: imm, str: text };
}
function body(name: string, params: typeinfo.Type[], types: typeinfo.Type[], ops: ssa.SInst[], ret: i32): ssasem.Func {
  let term = ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: ret };
  let graph = ssa.SFunc { name: name, nparams: params.len(), nvals: types.len(), entry: 0, takes_env: params.len() > 0,
    blocks: [ssa.SBlock { id: 0, preds: [], insts: ops, term: term }] };
  return ssasem.Func { graph: graph, values: types, params: params, result: word(), records: semrecords.no_records(),
    enums: [], calls: [], envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: false, dbg_vals: [], dbg_names: [] };
}
function callback(captured: boolean, reads_env: boolean, size: i32): ssasem.Func {
  let env: typeinfo.Type = typeinfo.TypeArray { elem: word(), view: false };
  if (captured) { env = ssasem.env_type("cb", [word()]); }
  let ops = [inst(ssasem.param(), 0, [], 0, ""), inst(ssasem.param(), 1, [], 1, "")];
  let types = [env, word()];
  if (reads_env) {
    ops = ops.append(inst(ssasem.length(), 2, [0], 0, ""));
    types = types.append(word());
  }
  let i: i32 = 0;
  while (i < size) {
    ops = ops.append(inst(1, types.len(), [], i, ""));
    types = types.append(word()); i = i + 1;
  }
  return body("cb", [env, word()], types, ops, 1);
}
function caller(cb: ssasem.Func, captured: boolean, alias: boolean, size: i32, calls: i32): ssasem.Func {
  let contract = ssasem.Contract { name: "cb", params: cb.params, modes: [ssasem.borrow_mode(), ssasem.value_mode()], result: word() };
  let fn_ty = ssasem.closure_type(contract);
  let args: i32[] = [];
  if (captured) { args = [1]; }
  let ops = [inst(1, 1, [], 42, ""), inst(ssasem.closure_new(), 0, args, 0, "cb")];
  let types = [fn_ty, word()];
  let target: i32 = 0;
  if (alias) {
    target = types.len(); types = types.append(fn_ty);
    ops = ops.append(inst(7, target, [0], 0, ""));
  }
  let i: i32 = 0;
  while (i < size) {
    ops = ops.append(inst(1, types.len(), [], i, ""));
    types = types.append(word()); i = i + 1;
  }
  i = 0;
  while (i < calls) {
    ops = ops.append(inst(ssasem.call_value(), types.len(), [target, 1], 0, ""));
    types = types.append(word()); i = i + 1;
  }
  let f = body("caller", [], types, ops, types.len() - 1);
  return ssasem.Func { ...f, calls: [contract] };
}
function count(f: ssasem.Func, kind: i32): i32 {
  let n: i32 = 0;
  for b in f.graph.blocks { for op in b.insts { if (op.kind_tag == kind) { n = n + 1; } } }
  return n;
}
function main(): i32 {
  // Positive, policy/noinline mask, unchanged caller, captured environment,
  // environment read, alias, leaf budget, caller budget, splice budget,
  // helper exposed by the earlier ordinary pass, and a noinline helper.
  let row: i32 = 0;
  while (row < 11) {
    let leaf_size: i32 = 1;
    if (row == 6) { leaf_size = 41; }
    let caller_size: i32 = 0;
    if (row == 7) { caller_size = 1500; }
    let calls: i32 = 1;
    if (row == 8) { calls = 65; }
    let cb = callback(row == 3, row == 4, leaf_size);
    let helper = body("helper", [word()], [word()], [inst(ssasem.param(), 0, [], 0, "")], 0);
    helper = ssasem.Func { ...helper, graph: ssa.SFunc { ...helper.graph, takes_env: false } };
    if (row >= 9) {
      let ops = [inst(ssasem.param(), 0, [], 0, ""), inst(ssasem.param(), 1, [], 1, ""), inst(ssasem.call(), 2, [1], 0, "helper")];
      cb = body("cb", cb.params, [cb.params[0], word(), word()], ops, 2);
      cb = ssasem.Func { ...cb, calls: [ssasem.Contract { name: "helper", params: [word()], modes: [ssasem.value_mode()], result: word() }] };
    }
    let f = caller(cb, row == 3, row == 5, caller_size, calls);
    let modes = [[ssasem.borrow_mode(), ssasem.value_mode()], [], [ssasem.value_mode()]];
    let out = seminline.inline_closures([cb, f, helper], ["cb", "caller", "helper"], modes, [row != 1, false, row != 10], [false, row != 2, false])[1];
    let want: i32 = 1;
    if (row == 0 || row == 9) { want = 0; }
    if (count(out, ssasem.call_value()) != want) { return 10 + row; }
    if (want == 0 && count(out, ssasem.closure_new()) != 0) { return 30; }
    if (want != 0 && count(out, ssasem.closure_new()) != 1) { return 31 + row; }
    let checked = ssasem.analyze(out);
    if (!checked.ok) { print(checked.why); return 50 + row; }
    row = row + 1;
  }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "seminline.fern")
	if err := os.WriteFile(filepath.Join(dir, "closure-inline.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "closure-inline.fern", "closure-inline")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("closure admission: %v\n%s", err, out)
	}
}
