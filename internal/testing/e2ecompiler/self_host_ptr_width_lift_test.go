package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__ptr_width()` on the native register path (#11555).
//
// The SSA lift took IR `ptr_width` through the flat-op fallback: an opaque
// value the register allocator treats as a call, materialised by the stack
// arm's push and pop. core/map reads it for every entry stride, so
// `__map_lookup_keyed` paid the push, the pop and the call-shaped clobber on
// every lookup. Both lifted ISAs are 64-bit, so the lift now makes it the
// constant 8. The map program checks the answers on both ISAs; the lift itself
// is checked directly, since on arm64 the flat op's result and a constant both
// spell `mov xN, #8` and only the register differs.
const ptrWidthLiftSrc = `import "core/map";

function main(): i32 {
    let m: Map[i32, i32] = Map { };
    let i: i32 = 0;
    while (i < 100) {
        m = m.insert(i * 7, i);
        i = i + 1;
    }
    let j: i32 = 0;
    while (j < 100) {
        if (m.get_or(j * 7, 0 - 1) != j) { return 1; }
        if (m.get_or(j * 7 + 1, 0 - 1) != 0 - 1) { return 2; }
        j = j + 1;
    }
    return 42;
}
`

func runPtrWidthLift(t *testing.T, target string) (string, int) {
	t.Helper()
	var runner, runPrefix, extra []string
	var driverBin, linkGcc string
	if target == "arm64-linux" {
		var qemu string
		_, runner, driverBin = buildModloadArm64DriverX86(t)
		linkGcc, qemu = arm64Tooling(t)
		if qemu != "" {
			runPrefix = []string{qemu}
		}
		extra = []string{"-target", "arm64-linux"}
	} else {
		linkGcc, runner, driverBin = buildModloadDriverX86(t)
		runPrefix = runner
	}
	asm, progDir := compileSourceModload(t, runner, driverBin, ptrWidthLiftSrc, extra...)
	if len(asm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "ptr_width_lift", asm)
	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return string(asm), cmd.ProcessState.ExitCode()
}

func TestSelfHostPtrWidthLiftX86_64(t *testing.T) {
	asm, code := runPtrWidthLift(t, "x86-64-linux")
	if code != 42 {
		t.Errorf("map program exited %d, want 42", code)
	}
	body := emittedBody(t, asm, "__fn___map_lookup_keyed")
	if strings.Contains(body, "pushq $8") {
		t.Errorf("__map_lookup_keyed still pushes the pointer width through the stack arm:\n%s", body)
	}
}

func TestSelfHostPtrWidthLiftArm64(t *testing.T) {
	if _, code := runPtrWidthLift(t, "arm64-linux"); code != 42 {
		t.Errorf("map program exited %d, want 42", code)
	}
}

// Lift `ptr_width; return` and read the instruction back: a constant 8 (SSA
// kind 1), not the flat op (kind 59).
const ptrWidthLiftOpsProg = `import "./ir";
import "./ssa";
import "./ssa_lift";

function main(): i32 {
    let ops: ir.Op[] = [ir.op_ptr_width(), ir.op_return()];
    let r: ssa_lift.LResult = ssa_lift.lift_from_ir_prod(ssa_lift.kind_table(), "width", 0, 0, ops, false);
    if (!r.ok) { return 2; }
    let n: i32 = 0;
    for blk in r.func.blocks {
        for ins in blk.insts {
            if (ins.kind_tag == 59) { return 3; }
            if (ins.kind_tag == 1 && ins.imm == 8) { n = n + 1; }
        }
    }
    if (n != 1) { return 4; }
    return 0;
}
`

func TestSelfHostSSALiftPtrWidthIsAConstant(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "ssa_lift_ptr_width.fern"), []byte(ptrWidthLiftOpsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "ssa_lift_ptr_width.fern", "ssa-lift-ptr-width")
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
		t.Fatal("the lift declined `ptr_width; return`")
	case 3:
		t.Fatal("ptr_width lifted as an opaque flat op, not a constant")
	case 4:
		t.Fatal("ptr_width did not lift to exactly one constant 8")
	default:
		t.Fatalf("driver exit %d: %s", code, output)
	}
}
