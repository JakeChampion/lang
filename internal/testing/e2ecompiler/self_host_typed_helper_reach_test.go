package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSelfHostTypedHelperReachability(t *testing.T) {
	const src = `import "./ir";
import "./irtables";
import "./physicalbody";
import "./semhelperreach";
import "./typeinfo";

function body(name: string, ops: ir.Op[]): irtables.LowerResult {
  return irtables.LowerResult { source_positions: irtables.no_source(), ok: true, ops: ops, n_locals: 0, n_params: 0,
    erased_wide: false, superseded: false, arr_slots: [], i64_slots: [], f64_slots: [], str_slots: [], v128_slots: [],
    why: "", name: name, result_kind: 0, dbg_slots: [], dbg_names: [], dbg_types: [] };
}

function origins(extra: irtables.LowerResult[], typed: boolean[]): physicalbody.Origin[] {
  let out: physicalbody.Origin[] = [];
  for i in 0..typed.len() {
    let helper: Option[physicalbody.Helper] = None;
    if (typed[i]) {
      helper = Some(physicalbody.Helper { ty: typeinfo.TypeStruct { name: "Parent", args: [] }, children_only: false });
    }
    out = out.append(physicalbody.Origin { producer: 0, symbol: extra[i].name, helper: helper });
  }
  return out;
}

function check(roots: irtables.LowerResult[], extra: irtables.LowerResult[], typed: boolean[], expected: string): boolean {
  let kept = semhelperreach.select(roots, extra, origins(extra, typed));
  let names = "";
  for i in 0..kept.bodies.len() {
    if (kept.bodies[i].name != extra[kept.indices[i]].name) { return false; }
    names = names + kept.bodies[i].name + ";";
  }
  return names == expected;
}

function main(): i32 {
  let helpers = [body("drop", []), body("release", [ir.op_call_direct("drop", 1)])];
  if (!check([body("main", [])], helpers, [true, true], "")) { return 1; }
  if (!check([body("main", [ir.op_call_direct("release", 1)])], helpers, [true, true], "drop;release;")) { return 2; }
  if (!check([body("main", [ir.op_const_func(0, "release")])], helpers, [true, true], "drop;release;")) { return 3; }
  if (!check([body("main", [ir.op_const_closure("release")])], helpers, [true, true], "drop;release;")) { return 4; }
  if (!check([body("main", [ir.op_const_struct("&arr", 1, "{&fn:release}")])], helpers, [true, true], "drop;release;")) { return 5; }
  if (!check([body("main", [ir.op_dyn_dispatch(["release", "other"], 0, 1)])], helpers, [true, true], "drop;release;")) { return 6; }
  let cycle = helpers.with(0, body("drop", [ir.op_call_direct("release", 1)]));
  if (!check([], cycle, [true, true], "")) { return 7; }
  if (!check([body("main", [ir.op_call_direct("release", 1)])], cycle, [true, true], "drop;release;")) { return 8; }
  let ordinary = helpers.append(body("wrapper", [ir.op_call_direct("release", 1)]));
  if (!check([], ordinary, [true, true, false], "drop;release;wrapper;")) { return 9; }
  if (!check([], helpers, [], "drop;release;")) { return 10; }
  let failed = helpers.with(1, irtables.LowerResult { ...helpers[1], ok: false, why: "failure control" });
  if (!check([], failed, [true, true], "release;")) { return 11; }
  let superseded = [irtables.LowerResult { ...body("main", [ir.op_call_direct("release", 1)]), superseded: true }];
  if (!check(superseded, helpers, [true, true], "")) { return 12; }
  return 0;
}`
	dir := writeSelfHostAsmProject(t)
	entry := filepath.Join(dir, "helper_reach.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var run *exec.Cmd
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		run = exec.Command(buildSelfHostBinArm64Darwin(t, dir, "helper_reach.fern", "helper_reach"))
	} else {
		gcc, runner := x86_64Tooling(t)
		run = runX86_64Bin(runner, buildSelfHostBin(t, gcc, dir, "helper_reach.fern", "helper_reach"))
	}
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("typed helper reachability: %v\n%s", err, out)
	}
}
