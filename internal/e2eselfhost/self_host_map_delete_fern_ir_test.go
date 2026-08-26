package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// `__fern_map_delete` is a Fern runtime source now (#2649), replacing the two
// hand-written asm bodies. It is the last of the map ops to move, and the one
// that needed the raw floor to grow a way to compare strings
// (`__fern_str_eq` as surface syntax) and a way to call a bare code address
// (an `i32` parameter, NOT an `fn` one).
//
// Each case deletes the middle key of three and then reads all three back, so
// a botched shift shows up as a wrong digit rather than a crash:
//
//	173 = a:1 kept * 100 + b: MISSING so the 7 default * 10 + c:3 kept
//
// 80 and 81 are the `existed` flag in each direction — a delete that reports
// nothing removed, and a miss that claims it removed something.
const (
	mapDelStrSrc = `function main(): i32 {
    var m: Map[string, i32] = Map {};
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    var r: (Map[string, i32], boolean) = m.without("b");
    if (!r.1) { return 80; }
    var n: Map[string, i32] = r.0;
    if (n.without("zz").1) { return 81; }
    return n.get_or("a", 0) * 100 + n.get_or("b", 7) * 10 + n.get_or("c", 0);
}
`
	mapDelI32Src = `function main(): i32 {
    var m: Map[i32, i32] = Map {};
    m = m.insert(10, 1);
    m = m.insert(20, 2);
    m = m.insert(30, 3);
    var r: (Map[i32, i32], boolean) = m.without(20);
    if (!r.1) { return 80; }
    var n: Map[i32, i32] = r.0;
    if (n.without(99).1) { return 81; }
    return n.get_or(10, 0) * 100 + n.get_or(20, 7) * 10 + n.get_or(30, 0);
}
`
	// The struct key is the case that matters most: it is the only one that
	// reaches the derived `__fn_K__eq` through a runtime code address, which
	// is what the `eqfn: i32` parameter exists for.
	mapDelStructSrc = `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct K { a: i32, b: i32 }
function main(): i32 {
    var m: Map[K, i32] = Map {};
    m = m.insert(K { a: 1, b: 1 }, 1);
    m = m.insert(K { a: 2, b: 2 }, 2);
    m = m.insert(K { a: 3, b: 3 }, 3);
    var r: (Map[K, i32], boolean) = m.without(K { a: 2, b: 2 });
    if (!r.1) { return 80; }
    var n: Map[K, i32] = r.0;
    if (n.without(K { a: 9, b: 9 }).1) { return 81; }
    return n.get_or(K { a: 1, b: 1 }, 0) * 100 + n.get_or(K { a: 2, b: 2 }, 7) * 10 + n.get_or(K { a: 3, b: 3 }, 0);
}
`
)

func mapDelCases() []struct{ name, src string } {
	return []struct{ name, src string }{
		{"string_key", mapDelStrSrc},
		{"i32_key", mapDelI32Src},
		{"struct_key", mapDelStructSrc},
	}
}

// TestSelfHostMapDeleteFernIRX86_64 runs each key kind on x86-64. 173 is the
// interpreter's answer for all three, taken as the oracle.
func TestSelfHostMapDeleteFernIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapDelCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir"))
			if len(asm) == 0 {
				t.Fatal("self-host emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "md_"+tc.name, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != 173 {
				t.Errorf("exited %d, want 173 (80=delete reported nothing removed, "+
					"81=a miss claimed a removal, other=the shift moved the wrong element)", got)
			}
		})
	}
}

// TestSelfHostMapDeleteFernIRArm64 is the same three programs under qemu. The
// helper source is shared, but the op site that marshals its four arguments is
// per-backend, so arm64 has to run them too.
func TestSelfHostMapDeleteFernIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapDelCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux"))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "md_arm64_"+tc.name, asm)
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != 173 {
				t.Errorf("arm64 exited %d, want 173", got)
			}
		})
	}
}

// TestSelfHostMapDeleteHandAsmGone pins the deletion. It keys on the hand-asm's
// own local labels (`.Lmd_loop_struct`, `.Lmd_kshift`, …) rather than on the
// symbol: `__fn___fern_map_delete:` CONTAINS `__fern_map_delete:` as a
// substring, so the obvious spelling of this assertion fires on the Fern helper
// it is meant to accept. Nothing but the deleted bodies ever emitted `.Lmd_`.
func TestSelfHostMapDeleteHandAsmGone(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, target string }{
		{"x86_64", ""},
		{"arm64", "arm64-linux"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"-ir"}
			if tc.target != "" {
				args = []string{"-target", tc.target}
			}
			asm := string(runCapture(t, gcc, runner, driverBin, []byte(mapDelStrSrc), args...))
			if !strings.Contains(asm, "__fn___fern_map_delete") {
				t.Fatalf("%s: the Fern helper is absent — this check would pass vacuously", tc.name)
			}
			if strings.Contains(asm, ".Lmd_") {
				t.Errorf("%s: the hand-asm map-delete body is back (its .Lmd_ labels are in the output)", tc.name)
			}
		})
	}
}
