package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The emitter-shape gates for asm_arm64_ir.fern's P4 and P5 peepholes and
// the i64 constant form — the arm64 twin of self_host_peephole_fold_test.go.
//
// Each case asserts two things that have to travel together: the folded form
// is present in the function that produces it and the round trip it replaces
// is absent, AND the program still exits with the value the unfolded path
// gives it. Either alone is easy to satisfy wrongly. The program is linked by
// the cross gcc and run under qemu, so every folded form is also assembled by
// an independent assembler.
//
// The shapes are asserted per FUNCTION BODY rather than over the whole
// module, because the hand-written runtime carries its own `mov x1, x0` and
// `add x0, x0, #N` lines that are not this pass's subject.

func arm64PeepholeFoldHarness(t *testing.T) (func(t *testing.T, src string) string, func(t *testing.T, name, asm string) int) {
	t.Helper()
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(driverBin, "-target", "arm64-linux")
		} else {
			args := append(append([]string{}, x86runner[1:]...), driverBin, "-target", "arm64-linux")
			cmd = exec.Command(x86runner[0], args...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(out)
	}

	run := func(t *testing.T, name, asm string) int {
		t.Helper()
		asmPath := filepath.Join(dir, name+".s")
		binPath := filepath.Join(dir, name)
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(arm64gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		inner := runArm64Bin(qemu, binPath)
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally", name)
		}
		return inner.ProcessState.ExitCode()
	}
	return emit, run
}

func runArm64PeepholeFoldCases(t *testing.T, cases []peepholeFoldCase) {
	t.Helper()
	emit, run := arm64PeepholeFoldHarness(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := emit(t, tc.src)
			for fn, lines := range tc.has {
				body := peepholeFnBody(t, asm, fn)
				for _, l := range lines {
					if !strings.Contains(body, l) {
						t.Errorf("%s: missing %q:\n%s", fn, l, body)
					}
				}
			}
			for fn, lines := range tc.lacks {
				body := peepholeFnBody(t, asm, fn)
				for _, l := range lines {
					if strings.Contains(body, l) {
						t.Errorf("%s: still carries %q:\n%s", fn, l, body)
					}
				}
			}
			if got := run(t, tc.name, asm); got != tc.want {
				t.Errorf("exit = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSelfHostConstOperandReachesImmediateFormArm64 pins P4: an add, sub or
// compare against a literal reaches the imm12 form, and the constant's trip
// through x1 is gone. imm12 holds 0..4095 or a multiple of 4096 up to
// 0xFFF000, so the refusals are pinned too: a constant outside that range
// keeps the register form, and P5 then materialises it straight into x1.
func TestSelfHostConstOperandReachesImmediateFormArm64(t *testing.T) {
	runArm64PeepholeFoldCases(t, []peepholeFoldCase{
		{
			name: "alu",
			src: `function bump(x: i64): i64 { return x + 1i64; }
function down(x: i32): i32 { return x - 4096; }
function less(x: i32): boolean { return x < 7; }
function main(): i32 {
    var i: i64 = 0i64; var s: i64 = 0i64;
    while (i < 3i64) { s = s + bump(i); if (less(i as i32)) { s = s + 100i64; } i = i + 1i64; }
    return ((s % 100i64) as i32) + down(4100);
}`,
			want: (300+1+2+3)%100 + 4,
			has: map[string][]string{
				"bump": {"add x0, x0, #1"},
				"down": {"sub x0, x0, #4096"},
				"less": {"cmp x0, #7\n    cset x0, lt"},
			},
			lacks: map[string][]string{
				"bump": {"mov x1, x0", "add x0, x0, x1", "str x0, [sp, #-16]!", "ldr x0, ="},
				"down": {"mov x1, x0", "sub x0, x0, x1"},
				"less": {"mov x1, x0", "cmp x0, x1"},
			},
		},
		{
			name: "refused-widths",
			src: `function odd(x: i32): i32 { return x + 4097; }
function wide(x: i64): i64 { return x + 70000i64; }
function main(): i32 { return odd(1) - 4000 + ((wide(1i64) % 100i64) as i32); }`,
			want: 98 + 1,
			has: map[string][]string{
				"odd":  {"mov x1, #4097\n    add x0, x0, x1"},
				"wide": {"ldr x1, =70000\n    add x0, x0, x1"},
			},
			lacks: map[string][]string{
				"odd":  {"add x0, x0, #4097", "mov x1, x0", "str x0, [sp, #-16]!"},
				"wide": {"add x0, x0, #70000", "mov x1, x0", "str x0, [sp, #-16]!"},
			},
		},
	})
}

// TestSelfHostOperandStackRoundTripFoldsArm64 pins P5: the push that
// protected x0 across a materialisation nothing disturbed folds away and the
// materialisation writes x1 directly — a local, a string literal's
// adrp/add pair, and a field read through a local, the chain form whose
// first line reads the pushed value itself.
func TestSelfHostOperandStackRoundTripFoldsArm64(t *testing.T) {
	runArm64PeepholeFoldCases(t, []peepholeFoldCase{
		{
			name: "local-and-literal",
			src: `struct Rec { name: string, n: i32 }
function both(a: i32, b: i32): i32 { return a - b; }
function is_ab(s: string): boolean { return s == "ab"; }
function pick(r: Rec, k: i32): i32 { return k + r.n; }
function main(): i32 {
    var r: Rec = Rec { name: "ab", n: 30 };
    var m: i32 = 0;
    if (is_ab(r.name)) { m = m + 1; }
    if (!is_ab("x")) { m = m + 2; }
    return both(50, 7) + pick(r, 4) + m;
}`,
			want: 43 + 34 + 3,
			has: map[string][]string{
				"both":  {"ldr x0, [x29, #-8]\n    ldr x1, [x29, #-16]\n    sub x0, x0, x1"},
				"is_ab": {"ldr x0, [x29, #-8]\n    adrp x1, .SB0\n    add x1, x1, :lo12:.SB0\n    str x1, [sp, #-16]!\n    str x0, [sp, #-16]!\n    bl __fn___fern_str_eq"},
				"pick":  {"ldr x0, [x29, #-16]\n    ldr x1, [x29, #-8]\n    ldr x1, [x1, #16]\n    add x0, x0, x1"},
			},
			lacks: map[string][]string{
				"both":  {"mov x1, x0", "str x0, [sp, #-16]!"},
				"is_ab": {"mov x1, x0", "adrp x0"},
				"pick":  {"mov x1, x0", "str x0, [sp, #-16]!"},
			},
		},
	})
}
