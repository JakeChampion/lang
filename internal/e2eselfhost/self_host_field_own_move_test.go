package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fieldOwnMoveCases pin the self-host lowering of the superseded-field move
// (#8186): `a = Asm { ...a, cfi: record(a.cfi, v) }` with `record(own s: Cfi,
// …)` hands `a.cfi` to the callee as a MOVE out of a's box. The call site
// tests __fern_rc_is_unique(a): a unique box has its slot emptied, so every
// later release of it meets a null; a shared box keeps its field and the
// callee is retained into. Both branches run here — the loop is the unique
// one, `shared` aliases the box first — on the rebind form, the return form,
// an own-param base and a local base.
//
// The exit code folds a value check (each mismatch its own bit) with
// __rc_underflow_count(), so a wrong value, an over-release and a crash each
// read as their own non-zero.
var fieldOwnMoveCases = []struct {
	name string
	src  string
	want int
}{
	{"field-move-value-and-rc", `struct Cfi { rules: i32[], n: i32 }
struct Asm { code: i32[], cfi: Cfi }
function record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }
function step(own a: Asm, v: i32): Asm {
    a = Asm { ...a, cfi: record(a.cfi, v) };
    a = Asm { ...a, code: a.code.append(v) };
    return a;
}
function step_ret(own a: Asm, v: i32): Asm { return Asm { ...a, cfi: record(a.cfi, v) }; }
function shared(v: i32): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [1, 2], n: 2 } };
    var keep: Asm = a;
    a = Asm { ...a, cfi: record(a.cfi, v) };
    return keep.cfi.n * 100 + a.cfi.n + keep.cfi.rules.len() * 1000;
}
function local_form(n: i32): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    var i: i32 = 0;
    while (i < n) {
        a = Asm { ...a, cfi: record(a.cfi, i) };
        i = i + 1;
    }
    return a.cfi.n + a.cfi.rules[n - 1];
}
function main(): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    var i: i32 = 0;
    while (i < 200) {
        a = step(a, i);
        a = step_ret(a, i);
        i = i + 1;
    }
    var bad: i32 = 0;
    if (a.cfi.n != 400) { bad = bad + 1; }
    if (a.cfi.rules.len() != 400) { bad = bad + 2; }
    if (a.code.len() != 200) { bad = bad + 4; }
    if (a.cfi.rules[399] != 199) { bad = bad + 8; }
    if (a.code[199] != 199) { bad = bad + 16; }
    if (shared(7) != 2203) { bad = bad + 32; }
    if (local_form(50) != 99) { bad = bad + 64; }
    return bad + __rc_underflow_count();
}`, 0},
}

const fieldOwnMoveFailFmt = "%s = %d, want %d (bits 1..64 name the value that came back wrong; anything else is the rc underflow count or a crash)"

func TestSelfHostFieldOwnMoveIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fieldOwnMoveCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// The value check passes on the retain fallback too, so pin the
			// move itself: inside `step`, the uniqueness test precedes the
			// call that consumes the field.
			if !uniqueTestBeforeCallInFn(string(asm), "__fn_step:", "call __fn_record") {
				t.Errorf("%s: `step` passes a.cfi to record's own parameter without the is_unique-gated move", tc.name)
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
				t.Errorf(fieldOwnMoveFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// uniqueTestBeforeCallInFn reports whether, in the asm text of the function
// starting at `label`, a __fern_rc_is_unique call appears before the first
// `callLine` and after any earlier non-runtime call.
func uniqueTestBeforeCallInFn(asm, label, callLine string) bool {
	start := strings.Index(asm, "\n"+label)
	if start < 0 {
		return false
	}
	body := asm[start:]
	end := strings.Index(body, callLine)
	if end < 0 {
		return false
	}
	return strings.Contains(body[:end], "__fern_rc_is_unique")
}

func TestSelfHostFieldOwnMoveIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fieldOwnMoveCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(fieldOwnMoveFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostFieldOwnMoveWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping field own move wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range fieldOwnMoveCases {
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
				t.Errorf(fieldOwnMoveFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
