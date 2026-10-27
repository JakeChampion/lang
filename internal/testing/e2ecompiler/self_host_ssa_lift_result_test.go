package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ssaLiftResultProg = `import "./ir";
import "./ssa";
import "./ssa_lift";

function refused(ops: ir.Op[], why: string): boolean {
  match (ssa_lift.lift_with_lanes(ssa_lift.kind_table(), "refused", 0, 0, ops, true, [0])) {
    Ok(_) => { print("accepted invalid " + why); return false; },
    Err(message) => {
      if (message != why) { print("refused " + why + " as " + message); }
      return message == why;
    }
  }
}

function main(): i32 {
  if (!refused([ir.Op { ...ir.op_const_i32(0), kind_tag: -1 }], "invalid#-1")) { return 1; }
  if (!refused([ir.op_drop()], "drop")) { return 2; }
  if (!refused([ir.op_const_i32(1), ir.op_if(0)], "unbalanced-scope")) { return 3; }
  if (!refused([ir.op_load_local(1)], "load_local")) { return 4; }
  if (!refused([ir.op_const_i32(1), ir.op_if(0), ir.op_end(), ir.op_drop()], "drop")) { return 12; }
  let kt = ssa_lift.kind_table();
  let empty = match (ssa_lift.lift_from_ir_prod(kt, "empty", 0, 0, [], false)) { Ok(l) => l, Err(_) => { return 5; } };
  if (empty.func.name != "empty" || empty.func.blocks.len() != 1) { return 5; }
  if (empty.seen_vals.len() != 0 || empty.seen_slots.len() != 0 || empty.source_returns.len() != 0 || empty.source_values.len() != 0) { return 6; }
  let value_pos = (41i64 << 32) | 5i64;
  let return_pos = (42i64 << 32) | 7i64;
  let ops = [ir.Op { ...ir.op_const_i32(8), source: value_pos }, ir.op_store_local(0), ir.op_load_local(0), ir.Op { ...ir.op_return(), source: return_pos }];
  let located = match (ssa_lift.lift_with_lanes(kt, "located", 1, 1, ops, true, [0])) { Ok(l) => l, Err(_) => { return 7; } };
  if (located.func.nparams != 1 || located.func.nvals != 2) { return 7; }
  if (located.func.blocks[0].insts[0].str != ssa.lane_param_mark()) { return 8; }
  if (located.seen_vals.len() != 2 || located.seen_vals[0] != 1 || located.seen_vals[1] != 1 || located.seen_slots[0] != 0 || located.seen_slots[1] != 0) { return 9; }
  if (located.source_values.len() != 2 || located.source_values[1] != value_pos) { return 10; }
  if (located.source_returns.len() != 1 || located.source_returns[0].block != 0 || located.source_returns[0].line != 42 || located.source_returns[0].col != "7") { return 11; }
  let fallback = match (ssa_lift.lift_from_ir_prod(kt, "fallback", 0, 0, [ir.op_const_str("text"), ir.op_drop(), ir.op_arr_make(0, 8)], false)) { Ok(l) => l, Err(_) => { return 17; } };
  if (fallback.func.blocks[0].insts[0].kind_tag != 47 || fallback.func.blocks[0].insts[0].str != "text") { return 18; }
  let failures: i32 = 0;
  if (!refused([ir.op_struct_make("Pair", 2, 0)], "struct_make")) { failures = failures + 1; }
  if (!refused([ir.op_const_i32(1), ir.op_struct_make("Pair", 2, 0)], "struct_make")) { failures = failures + 1; }
  if (!refused([ir.op_arr_make(2, 8)], "arr_make")) { failures = failures + 1; }
  if (!refused([ir.op_arr_make(2, 1)], "arr_make")) { failures = failures + 1; }
  if (!refused([ir.op_tuple_make(2)], "tuple_make")) { failures = failures + 1; }
  if (!refused([ir.op_opt_make(1)], "opt_make")) { failures = failures + 1; }
  if (failures != 0) { return 19; }
  return 0;
}
`

func TestSelfHostSSALiftResultContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_lift.fern")
	writeResolveProject(t, dir, map[string]string{"lift_result.fern": ssaLiftResultProg})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "lift_result.fern", "lift_result", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("SSA lift contract: %v\n%s", err, out)
			}
		})
	}
}
