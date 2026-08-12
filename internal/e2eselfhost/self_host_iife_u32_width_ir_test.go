package e2eselfhost

import (
	"os/exec"
	"testing"
)

// iifeU32WidthIRCases pin the WIDTH a value-position `if`/`match` reports when
// a branch is a suffixed literal past i32-max — #6400.
//
// iife_any_return_i64 re-derived the width from magnitude alone, so a `u32`
// literal above i32-max reported 64. That width feeds lower_checked_binary's
// operand kind, and a `u32` read as i64 selects the SIGNED overflow predicate
// (`a > I64_MAX + b`), which cannot fire for operands that fit in i64 as
// positives — `-?` then answers Some with the wrapped difference.
//
// Every case asserts the ANSWER. The defect was a silent miscompile on one path
// and a bail on another, and a compile-only assertion catches neither.
var iifeU32WidthIRCases = []struct {
	name string
	src  string
	exit int
}{
	// Reduced from seed s0056: a value-position `match` whose arm is a u32
	// literal past i32-max. Reported 64, so the enclosing function refused;
	// it now lowers and answers. Verified to BAIL on the parent commit.
	{"u32-literal-arm-past-i32max", `enum E1 { __E1_V0, __E1_V1 }
function gen_f0(p2: u32): u32 { var v0: (i32, i64) = ((((941i32 -| 648i32), (520i64)))); var v1: Option[i32] = (Some((match ((v0.0) /? ((v0.0 % 65i32))) { Some(__chk_v0) => __chk_v0, None => v0.0 }))); return (p2 << (match (v1) { Some(__opt_x1) => 2147484624u32, None => (779u32) })); }
function main(): i32 { return ((gen_f0(5u32) >> 16u32) as i32) & 63i32; }`, 5},
	// Control: a BARE wide literal in an if-expression — the shape the removed
	// magnitude test named in its own comment. No suffix, so magnitude is the
	// only signal and the branch really is 64-bit; infer_expr_width says so.
	{"bare-wide-literal-arms-control", `function main(): i32 {
    var v: i64 = (if (true) { 5000000000 } else { 1 });
    return ((v / 1000000000) as i32) & 63i32;
}`, 5},
	// Control: a u64 suffix, where 64 IS the right width. Pins that reading the
	// suffix did not collapse into "every suffix means 32".
	{"u64-suffix-arms-control", `function main(): i32 {
    var v: u64 = (if (true) { 18000000000000000000u64 } else { 1u64 });
    return ((v / 1000000000000000000u64) as i32) & 63i32;
}`, 18},
	// Control: the checked sub whose scrutinee holds a u32 if-expression, with a
	// LITERAL None arm. This answered correctly before the fix and must keep
	// doing so — the arm is what decides whether the outer match stays an
	// un-lifted IIFE, so this row isolates the arm as the trigger.
	{"literal-arm-control", `function main(): i32 {
    var w: u32 = (match ((2147484129u32) -? (if (true) { 2147484571u32 } else { 250u32 })) { Some(c) => c, None => 5u32 });
    return (w as i32) & 63i32;
}`, 5},

	// The LOWERING half of #6400, which the width fix above left refusing: a
	// bare u32 Some payload (`Some(c) => c`) in a value-position match. The
	// inlined match-expression's payload gate named i32 and nothing else, so
	// the u32 spelling bailed where the i32 one lowered — even though a u32
	// payload rides the same 32-bit slot and the STATEMENT form of the same
	// match already lowered.
	//
	// The first row is the issue's repro verbatim. It needs the OTHER arm to be
	// a capturing value-position `if`, which is what keeps the outer match an
	// un-lifted IIFE and so on this gate at all — with a literal arm it takes
	// the __lam_N hoist instead (literal-arm-control above).
	{"issue-6400-repro", `function main(): i32 {
    var b: boolean = true;
    var w: u32 = (match ((2147484129u32) -? (if (true) { 2147484571u32 } else { 250u32 })) { Some(c) => c, None => (if (b) { 5u32 } else { 687u32 }) });
    return (w as i32) & 63i32;
}`, 5},
	{"u32-payload-none-arm-taken", `function main(): i32 {
    var b: boolean = true;
    var w: u32 = (match ((100u32) -? (200u32)) { Some(c) => c, None => (if (b) { 5u32 } else { 687u32 }) });
    return (w as i32) & 63i32;
}`, 5},
	{"u32-payload-some-arm-taken", `function main(): i32 {
    var b: boolean = true;
    var w: u32 = (match ((4000000000u32) -? (3999999995u32)) { Some(c) => c, None => (if (b) { 9u32 } else { 7u32 }) });
    return (w as i32) & 63i32;
}`, 5},
	// A payload above i32-max, so the value has to survive the i32-marked
	// result temp intact rather than being read as a negative.
	{"u32-payload-above-i32max", `function main(): i32 {
    var b: boolean = true;
    var w: u32 = (match ((4294967290u32) -? (2u32)) { Some(c) => c, None => (if (b) { 9u32 } else { 7u32 }) });
    return (w as i32) & 63i32;
}`, 56},
	// And the result still compares UNSIGNED after the round trip — the check
	// that the i32 temp is a carrier and not a reinterpretation.
	{"u32-result-compares-unsigned", `function f(): Option[u32] { return Some(4294967290u32); }
function main(): i32 {
    var b: boolean = true;
    var w: u32 = (match (f()) { Some(c) => c, None => (if (b) { 9u32 } else { 7u32 }) });
    if (w > 2147483648u32) { return 1i32; }
    return 2i32;
}`, 1},
	// An Option[u32] LOCAL rather than a checked-arithmetic scrutinee, so the
	// gate is reached through try_opt_type's annotation path too.
	{"optu32-local-scrutinee", `function main(): i32 {
    var b: boolean = true;
    var o: Option[u32] = None;
    var w: u32 = (match (o) { Some(c) => c, None => (if (b) { 9u32 } else { 7u32 }) });
    return (w as i32) & 63i32;
}`, 9},
	// Control: the i32 spelling of the first row, which lowered all along. It
	// is what made the u32 refusal a width gap rather than a shape gap.
	{"i32-spelling-control", `function main(): i32 {
    var b: boolean = true;
    var w: i32 = (match ((100i32) -? (200i32)) { Some(c) => c, None => (if (b) { 5i32 } else { 687i32 }) });
    return w & 63i32;
}`, 28},
}

// TestSelfHostIifeU32WidthIRX86_64 drives the production x86-64 IR path.
func TestSelfHostIifeU32WidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range iifeU32WidthIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostIifeU32WidthIRArm64 — the arm64 counterpart. The width is computed
// in the shared lowering, so both backends behaved identically; running it is
// what proves that rather than assuming it.
func TestSelfHostIifeU32WidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 IIFE-u32-width gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range iifeU32WidthIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
