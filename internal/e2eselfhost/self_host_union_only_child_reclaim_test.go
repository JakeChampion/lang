package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// unionOnlyChildReclaimCases pin a tuple whose ONLY rc child is a tagged union.
//
// #7147 taught emit_tuple_child_drops to release a union constructed at an
// element position, but nothing COUNTED such an element as an rc child, so
// tuple_lit_has_rc_child answered false and the tuple was never admitted at all
// — its own box leaked alongside the union's. Measured per round before, as
// x86-64 | arm64 | wasm, native flat on both rows: 80 | 80 | 40 for
// `(i, Some(i))` and 80 | 80 | 48 for a user-enum variant.
//
// The count is driven by the element's DECLARED TAG, not by its expression.
// That is what keeps it accurate, and accuracy matters in one specific way: a
// tuple carrying a union position is never all-scalar, so this credit and the
// shallow scalar-tuple path can never both claim the same local and free its
// box twice. A name-based test for `Some` could not offer that guarantee,
// because a user function may be called `Some`.
//
// An un-annotated binding yields an empty tag and is left exactly as it was.
var unionOnlyChildReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"option-only-rc-child-reclaimed", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Option[i32]) = (i, Some(i));
        var r: i32 = t.0;
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// The user-enum tag resolves through decl_is_enum rather than the built-in
	// `Option[` / `Result[` prefixes, so it is a separate path from the row above.
	{"user-enum-only-rc-child-reclaimed", `enum Tag { Num(i32), Nil }
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Tag) = (i, Tag.Num(i));
        var r: i32 = t.0;
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// ALIASING negative: the element is a bare IDENT of union type, not a
	// construction, so the count must not fire — the box belongs to `o`, which
	// is read after the tuple's reclaim point. Crediting here would free a live
	// local's box: an over-release, not a leak.
	{"ident-union-elem-not-credited", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var o: Option[i32] = Some(i);
        var t: (i32, Option[i32]) = (i, o);
        var r: i32 = t.0;
        match (o) { Some(v) => { r = r + v; }, None => {} }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 2},
}

const unionOnlyChildFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostUnionOnlyChildReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range unionOnlyChildReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(unionOnlyChildFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostUnionOnlyChildReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range unionOnlyChildReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(unionOnlyChildFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostUnionOnlyChildReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping union-only-child reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range unionOnlyChildReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf(unionOnlyChildFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
