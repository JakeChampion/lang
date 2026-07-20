package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Heap-bump FIXPOINTS for the self-host x86-64 IR path's `.len()` CALL-
// receiver reclaim — the self-host twin of native #5141 (#4357 fallout). A
// user-call result consumed as a len receiver (`f(i).len()`) leaked the
// callee's returned value linearly (strings ~160 B/iter, arrays ~104 B/iter):
// the len lowering's receiver reclaim only matched direct concat receivers.
//
// Since this path has no return-transfer inc, only registry-proven-fresh
// callees are freed: string receivers gate on "SFRLEN:" seeds
// (str_fresh_ret_fns ∩ the per-body len-receiver-callee walker) and release
// via the rc-aware __fern_str_free; array receivers gate on the strict-fresh
// "ARR:" entries in return_fresh_struct_ret_fns and release via the
// rc-guarded __fern_rc_dec. Unproven callees (the identity negatives) keep
// the prior safe-leak, so live caller values are never touched.
//
// The builder shapes deliberately avoid literal-bound string LOCALS
// (`var p: string = "..."`): those leak their 24-byte box per call through a
// separate, pre-existing callee-side exit-sweep gap (bare-literal inits are
// excluded from the fresh classes) that would mask these fixpoints.
var lenRecvIRCases = []struct {
	name  string
	src   func(n string) string
	fixed bool
	want  int
}{
	{name: "str-recv", src: func(n string) string {
		return `function f(k: i32): string {
    return "prefix-block-aaaa" + "suffix-block-bbbb";
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + f(i).len(); i = i + 1; }
    if (acc != ` + n + ` * 34) { return 121; }
    var g: i32 = __heap_bump_bytes() - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "arr-recv", src: func(n string) string {
		return `function mk(k: i32): i32[] {
    return [k, k + 1, k + 2, k + 3, k + 4, k + 5, k + 6, k + 7, k + 8, k + 9];
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + mk(i).len(); i = i + 1; }
    if (acc != ` + n + ` * 10) { return 121; }
    var g: i32 = __heap_bump_bytes() - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	// Identity callees return the CALLER's value: neither is registry-fresh
	// (a bare-ident return is non-fresh for strings; not a direct array
	// literal for "ARR:"), so no free fires — base / arr stay value-intact.
	{name: "alias-negative", fixed: true, want: 0, src: func(string) string {
		return `function id(s: string): string { return s; }
function ida(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    var base: string = "0123456789abcdef" + "-suffix-to-force-heap";
    var arr: i32[] = [10, 20, 30, 40, 50, 60, 70, 80, 90, 100];
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 3000) { acc = acc + id(base).len() + ida(arr).len(); i = i + 1; }
    if (acc != 3000 * (37 + 10)) { return 121; }
    if (base.len() != 37) { return 122; }
    if (arr[9] != 100) { return 123; }
    return 0;
}`
	}},
}

// TestSelfHostLenReceiverIRX86_64 runs the shapes through the self-hosted
// x86-64 IR driver (asm_run). Fixpoint cases assert growth(N=50) ==
// growth(N=5000), non-zero, under the leak guard; the fixed case asserts its
// exact exit (121-123 = value corruption, 119 = leak guard).
func TestSelfHostLenReceiverIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	sh := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range lenRecvIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixed {
				if code := sh(t, tc.name, tc.src("")); code != tc.want {
					t.Errorf("%s: exited %d, want %d (121-123=value corruption, 119=leak guard)", tc.name, code, tc.want)
				}
				return
			}
			small := sh(t, tc.name+"-50", tc.src("50"))
			large := sh(t, tc.name+"-5000", tc.src("5000"))
			if small != large {
				t.Errorf("%s: high-water not bounded (N=50 -> %d, N=5000 -> %d)", tc.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: growth is 0 — probe does not allocate", tc.name)
			}
			if small >= 119 {
				t.Errorf("%s: leak guard tripped (%d)", tc.name, small)
			}
		})
	}
}
