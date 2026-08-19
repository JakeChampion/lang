package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tostringFreshRetCases pin a helper whose return is a scalar `.to_string()` —
// `function util_num(i: i32): string { return i.to_string(); }` — entering the
// whole-program fresh-ret registry, so `var sv = util_num(i)` reclaims the box
// the callee moved out.
//
// str_return_is_fresh delegated to str_local_binding_is_fresh, whose method arm
// admits only the fresh-allocating builtins on a STRING receiver. A scalar
// `.to_string()` is a different shape: is_fresh_str_temp has credited it since
// #4353 and its comment names str_local_binding_is_fresh as the predicate that
// missed it, but the registry consults exactly that predicate, so the helper
// never registered and every caller's binding leaked 32 B/round. Isolated by
// varying only the callee body, on x86-64:
//
//	return i.to_string();                32 B/round
//	var t = i.to_string(); return t;     32
//	return "x" + i.to_string();           0
//	return "abc";                         0
//
// The receiver test is state-free (tostring_recv_is_scalar_param): the registry
// pass walks FuncDecls with no LowerState, so a receiver ident resolves against
// the callee's DECLARED params. Only a provably scalar receiver admits — a
// struct `to_string` may hand back an alias of a live field.
var tostringFreshRetCases = []struct {
	name string
	src  string
	want int
}{
	// The gate. 32 before the fix on all three backends; native flat.
	{"freshret-tostring-direct-bind", `function util_num(i: i32): string {
    return i.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
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
	// The same helper as a CONCAT OPERAND rather than a direct bind — the shape
	// the leak was first spotted in.
	{"freshret-tostring-concat-operand", `function util_num(i: i32): string {
    return i.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = "v" + util_num(i);
        acc = (acc + sv.len()) % 91;
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
	// The callee binds the conversion to a LOCAL first and returns it. That path
	// runs through str_local_is_fresh_ret -> strloc_declared_fresh, which is why
	// the params thread has to reach the local-declaration test too.
	{"freshret-tostring-via-local", `function util_num(i: i32): string {
    var t: string = i.to_string();
    return t;
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
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
	// Non-vacuity control on the other side: a callee returning a CONCAT was
	// already registered and must stay at 0.
	{"freshret-concat-return-unchanged", `function util_num(i: i32): string {
    return "x" + i.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
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
	// REFUSAL control. The receiver is a STRUCT with a user `to_string`, whose
	// return is a live field — an alias, not a handover. tostring_recv_is_scalar_param
	// answers false on a non-scalar declared type, so the helper stays out of the
	// registry and the caller must not release. Over-release shows as 99.
	{"freshret-struct-tostring-refused", `struct Tag { s: string }
function (t: Tag) to_string(): string {
    return t.s;
}
function util_tag(t: Tag): string {
    return t.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var tg: Tag = Tag { s: "abc" };
        var sv: string = util_tag(tg);
        acc = (acc + sv.len() + tg.s.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 85},
	// REFUSAL control. `to_string` on a STRING receiver is the identity-ish
	// builtin, not the decimal-text producer: the result may be the receiver's
	// own box. A string-typed param is not a scalar, so this stays refused.
	{"freshret-string-tostring-refused", `function util_s(s: string): string {
    return s.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var base: string = "v" + i.to_string();
        var sv: string = util_s(base);
        acc = (acc + sv.len() + base.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 45},
}

const tostringFreshRetFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostTostringFreshRetIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tostringFreshRetCases {
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
				t.Errorf(tostringFreshRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTostringFreshRetIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tostringFreshRetCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tostringFreshRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTostringFreshRetWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping to_string fresh-ret wasm IR e2e")
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

	for _, tc := range tostringFreshRetCases {
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
			run := exec.Command("wasmtime", watFile)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tostringFreshRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}
