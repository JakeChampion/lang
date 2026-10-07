package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A closing scope in the SSA lift folds its slot rows into the enclosing
// scope, except a row whose slot nothing reads again: that one is dropped, so
// no enclosing merge builds a phi for it
// (docs/ssa-log/2026-10-07-i-a-closing-scope-drops-the-slots-nothing-reads-again.md).
// The driver lifts op streams directly and counts the phis:
//
//   - a temporary written in the innermost arm of three nested ifs, never
//     read after it, gets the innermost merge's phi and no other;
//   - the same temporary read after the outermost if keeps all three;
//   - a slot read at the top of a loop and written in an if further down is
//     read again through the back edge, so the if's fold keeps its row and
//     the header phi takes the if's merge on the back edge, not itself.
const ssaLiftDeadRowsProg = `import "./ir";
import "./ssa";
import "./ssa_lift";

function phis(f: ssa.SFunc): i32 {
    let n: i32 = 0;
    for b in f.blocks {
        for ins in b.insts {
            if (ins.kind_tag == 8) { n = n + 1; }
        }
    }
    return n;
}

// load cond; if {} else { load cond; if {} else { load cond; if { t = 5; read t } end } end } end
function chain(read_after: boolean): ir.Op[] {
    let ops: ir.Op[] = [
        ir.op_load_local(0), ir.op_if(0), ir.op_else(),
        ir.op_load_local(0), ir.op_if(0), ir.op_else(),
        ir.op_load_local(0), ir.op_if(0),
        ir.op_const_i32(5), ir.op_store_local(1), ir.op_load_local(1), ir.op_drop(),
        ir.op_end(), ir.op_end(), ir.op_end()
    ];
    if (read_after) {
        ops = ops.append(ir.op_load_local(1));
    } else {
        ops = ops.append(ir.op_const_i32(0));
    }
    return ops.append(ir.op_return());
}

// t = 0; loop { read t; if (cond) { t = 5 } end; br 0 } end; return 0
function looped(): ir.Op[] {
    return [
        ir.op_const_i32(0), ir.op_store_local(1),
        ir.op_loop(0),
        ir.op_load_local(1), ir.op_drop(),
        ir.op_load_local(0), ir.op_if(0), ir.op_const_i32(5), ir.op_store_local(1), ir.op_end(),
        ir.op_br(0),
        ir.op_end(),
        ir.op_const_i32(0), ir.op_return()
    ];
}

function main(): i32 {
    let kt: ssa_lift.KindTable = ssa_lift.kind_table();
    let dead: ssa_lift.LResult = ssa_lift.lift_from_ir_prod(kt, "dead", 1, 2, chain(false), false);
    if (!dead.ok) { return 2; }
    if (phis(dead.func) != 1) { return 10 + phis(dead.func); }
    let live: ssa_lift.LResult = ssa_lift.lift_from_ir_prod(kt, "live", 1, 2, chain(true), false);
    if (!live.ok) { return 3; }
    if (phis(live.func) != 3) { return 20 + phis(live.func); }
    let lp: ssa_lift.LResult = ssa_lift.lift_from_ir_prod(kt, "looped", 1, 2, looped(), false);
    if (!lp.ok) { return 4; }
    for b in lp.func.blocks {
        for ins in b.insts {
            if (ins.kind_tag == 8 && ins.args.len() == 2 && ins.args[1] == ins.result) { return 30; }
        }
    }
    return 0;
}
`

func TestSelfHostSSALiftDropsDeadRows(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "ssa_lift_dead_rows.fern"), []byte(ssaLiftDeadRowsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "ssa_lift_dead_rows.fern", "ssa-lift-dead-rows")
	output, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("ssa_lift_dead_rows exited %v (2-4 a lift refused; 1x the dead chain's phi count; 2x the live chain's; 30 the loop's back edge lost the if's value):\n%s", err, output)
	}
}
