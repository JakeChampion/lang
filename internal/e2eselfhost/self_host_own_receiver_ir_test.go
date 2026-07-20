package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ownReceiverCases exercise `own`-RECEIVER methods (`function (own s: Acc)
// emit(…)`) through the self-host compiler. The self-host parser must consume
// the `own` modifier in the receiver clause (before the fix the receiver
// branch read `own` AS the receiver name, left the cursor on the real name,
// and the decl misparsed); the backends then lower the receiver as a borrow
// (leak-safe — the native compiler's move semantics are not mirrored yet), so
// the programs' observable behaviour matches the native reference exit codes.
//
// Shapes mirror internal/e2e/rc_own_receiver_rebind_test.go's native pins:
// a local-receiver self-reassign churn, the borrowed-param threading shape,
// and a receiver literally named `own` (the parse_param-mirrored probe must
// not swallow it).
var ownReceiverCases = []struct {
	name string
	src  string
	exit int
}{
	{"own-recv-local-churn", `struct Acc { out: i32[], n: i32 }
pub function (own s: Acc) emit(x: i32): Acc {
    var ys = s.out.append(x);
    return Acc { out: ys, n: s.n + 1 };
}
function main(): i32 {
    var s = Acc { out: [], n: 0 };
    var i: i32 = 0;
    while (i < 40) { s = s.emit(i); i = i + 1; }
    if (s.out.len() != 40) { return 1; }
    if (s.out[39] != 39) { return 2; }
    return s.n;
}`, 40},
	{"own-recv-threaded", `struct Acc { out: i32[], n: i32 }
pub function (own s: Acc) emit(x: i32): Acc {
    var ys = s.out.append(x);
    return Acc { out: ys, n: s.n + 1 };
}
function emit_two(s: Acc, a: i32, b: i32): Acc {
    s = s.emit(a);
    s = s.emit(b);
    return s;
}
function main(): i32 {
    var s = Acc { out: [], n: 0 };
    var i: i32 = 0;
    while (i < 10) { s = emit_two(s, i, i + 1); i = i + 1; }
    if (s.out.len() != 20) { return 1; }
    return s.n + s.out[19];
}`, 30},
	{"receiver-named-own", `struct Box { v: i32 }
pub function (own: Box) get(): i32 { return own.v; }
function main(): i32 {
    var b = Box { v: 7 };
    return b.get();
}`, 7},
}

// TestSelfHostOwnReceiverIRX86_64 — own-receiver methods through the
// PRODUCTION x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostOwnReceiverIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ownReceiverCases {
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
