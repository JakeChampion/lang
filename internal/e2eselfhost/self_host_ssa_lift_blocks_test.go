package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// The SSA lift (examples/self_host/ssa_lift.fern) hands each emitter a block
// list, and the emitters write one `.Lssa_<fn>_<id>:` label per entry — so two
// entries carrying one id spell one label twice and the assembler rejects the
// module, naming neither the function nor the pass (#9688).
//
// A loop nothing leaves alive is where that happened: the loop's `end` appends
// the body's tail block, and the lift then stayed on that block's id, so the
// function's own tail appended it a second time. The driver lifts that op
// stream directly rather than going through a source program, because no loop
// anybody WRITES ends that way: the scope comes from irlower.tco_self_tail,
// which wraps a whole function body in `loop { … } end` so a self tail call
// jumps to the header, and a function body ends in a return. That is why the
// 55 collisions were all in the compiler's own modules — every one of those
// functions is self-recursive and none contains a source loop
// (docs/ssa-log/2026-09-18-where-the-lifts-duplicate-loop-came-from.md).
//
// A source program does reach it through that wrapper, on the leg where TCO runs
// (#9692): TestSelfHostSSALoopTailBlockEmittedOnce compiles one and assembles
// the listing.
const ssaLiftBlockIDProg = `// Assert the lift never returns two blocks with one id.
import "./ir";
import "./ssa";
import "./ssa_lift";

function distinct_block_ids(name: string, ops: ir.Op[]): i32 {
    var r: ssa_lift.LResult = ssa_lift.lift_from_ir_prod(name, 0, 0, ops);
    if (!r.ok) { return 2; }
    if (ssa.repeated_block_id(r.func) >= 0) { return 3; }
    return 0;
}

function main(): i32 {
    // loop { 0; return } end   0; return
    var returning_loop: ir.Op[] = [
        ir.op_loop(0),
        ir.op_const_i32(0),
        ir.op_return(),
        ir.op_end(),
        ir.op_const_i32(0),
        ir.op_return()
    ];
    var rc: i32 = distinct_block_ids("returning_loop", returning_loop);
    if (rc != 0) { return rc; }

    // The same loop followed by an ` + "`if`" + `, the other unconditional append.
    var loop_then_if: ir.Op[] = [
        ir.op_loop(0),
        ir.op_const_i32(0),
        ir.op_return(),
        ir.op_end(),
        ir.op_const_i32(1),
        ir.op_if(0),
        ir.op_const_i32(2),
        ir.op_return(),
        ir.op_end(),
        ir.op_const_i32(0),
        ir.op_return()
    ];
    rc = distinct_block_ids("loop_then_if", loop_then_if);
    if (rc != 0) { return rc + 10; }
    return 0;
}
`

func TestSelfHostSSALiftGivesEachBlockOneID(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "ssa_lift_blocks.fern"), []byte(ssaLiftBlockIDProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "ssa_lift_blocks.fern", "ssa-lift-blocks")
	cmd := runX86_64Bin(runner, bin)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("driver did not exit normally: %v\n%s", err, output)
	}
	switch code := cmd.ProcessState.ExitCode(); code {
	case 2, 12:
		t.Fatalf("the lift declined the op stream, so it proved nothing (exit %d)", code)
	case 3, 13:
		t.Fatalf("the lift returned two blocks with one id (exit %d) — one .Lssa_ label would be written twice", code)
	default:
		t.Fatalf("driver exit %d: %s", code, output)
	}
}

// A refusal names the op it refused on, even for a kind tag no op can carry,
// so the strict-SSA diagnostics can report the site instead of emitting a
// partial function.
const ssaLiftNegativeKindProg = `import "./ir";
import "./ssa_lift";

function main(): i32 {
    var ops: ir.Op[] = [ir.Op { ...ir.op_const_i32(0), kind_tag: 0 - 1 }, ir.op_return()];
    var r: ssa_lift.LResult = ssa_lift.lift_from_ir_prod("negative_kind", 0, 0, ops);
    if (r.ok) { return 2; }
    if (r.bail != "invalid#-1") { return 3; }
    return 0;
}
`

func TestSelfHostSSALiftNamesANegativeKind(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "ssa_lift_negative.fern"), []byte(ssaLiftNegativeKindProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "ssa_lift_negative.fern", "ssa-lift-negative")
	cmd := runX86_64Bin(runner, bin)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("driver did not exit normally: %v\n%s", err, output)
	}
	switch code := cmd.ProcessState.ExitCode(); code {
	case 2:
		t.Fatal("the lift accepted an op with a negative kind tag")
	case 3:
		t.Fatal("the lift refused a negative kind tag without naming it")
	default:
		t.Fatalf("driver exit %d: %s", code, output)
	}
}
