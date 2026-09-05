package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The emitter-shape gates for asm_ir.fern's P4, P5 and P6 peepholes and the
// 32-bit constant form — the self-host mirror of native's
// const_alu_fold_test.go, stacked_materialise_test.go, arg_materialise_test.go
// and const_zero_xor_test.go (internal/codegen/x86_64).
//
// Each case asserts two things that have to travel together: the folded form
// is present in the function that produces it and the round trip it replaces
// is absent, AND the program still exits with the value the interpreter gives
// it. Either alone is easy to satisfy wrongly.
//
// The shapes are asserted per FUNCTION BODY rather than over the whole module,
// because the hand-written runtime carries its own `movq %rax, %rcx` and
// `addq %rcx, %rax` lines that are not this pass's subject.

// peepholeFoldHarness builds the asm_ir_run driver once and returns an emit
// function (source -> asm text) and a run function (asm -> exit code).
func peepholeFoldHarness(t *testing.T) (func(t *testing.T, src string) string, func(t *testing.T, name, asm string) int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(binPath)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally", name)
		}
		return inner.ProcessState.ExitCode()
	}
	return emit, run
}

// peepholeFnBody returns the emitted body of `__fn_<name>`, up to its `ret`.
func peepholeFnBody(t *testing.T, asm, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)\n__fn_` + regexp.QuoteMeta(name) + `:\n(.*?)\n    ret\n`)
	m := re.FindStringSubmatch(asm)
	if m == nil {
		t.Fatalf("__fn_%s not found in emitted asm:\n%s", name, asm)
	}
	return m[1]
}

// peepholeFoldCase is one program with, per function, the lines its body must
// carry and the lines it must not.
type peepholeFoldCase struct {
	name string
	src  string
	want int
	// fn -> present lines
	has map[string][]string
	// fn -> absent lines
	lacks map[string][]string
}

func runPeepholeFoldCases(t *testing.T, cases []peepholeFoldCase) {
	t.Helper()
	emit, run := peepholeFoldHarness(t)
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

// TestSelfHostConstOperandReachesImmediateFormX86_64 pins P4: a binary
// operation against a literal reaches the immediate form, and the
// constant-into-%rcx handoff is gone. The two-operand imulq has no immediate
// form, so it takes the three-operand one; a shift whose count is a 64-bit
// literal folds to the imm8 form (a 32-bit count is already selected as one).
//
// Width is the rule's whole risk, so the refusals are pinned too: a literal
// past imm32 cannot fold, and must still compute the right answer through
// the register form.
func TestSelfHostConstOperandReachesImmediateFormX86_64(t *testing.T) {
	runPeepholeFoldCases(t, []peepholeFoldCase{
		{
			name: "alu",
			src: `function bump(x: i64): i64 { return x + 1i64; }
function scaled(x: i64): i64 { return x * 10i64; }
function masked(x: i32): i32 { return x & 6; }
function less(x: i32): boolean { return x < 7; }
function main(): i32 {
    var i: i64 = 0i64; var s: i64 = 0i64;
    while (i < 3i64) { s = s + bump(i) + scaled(i) + (masked(i as i32) as i64); if (less(i as i32)) { s = s + 100i64; } i = i + 1i64; }
    return (s % 100i64) as i32;
}`,
			want: (300 + (1 + 0 + 0) + (2 + 10 + 0) + (3 + 20 + 2)) % 100,
			has: map[string][]string{
				"bump":   {"addq $1, %rax"},
				"scaled": {"imulq $10, %rax, %rax"},
				"masked": {"andq $6, %rax"},
				"less":   {"cmpq $7, %rax"},
			},
			lacks: map[string][]string{
				"bump":   {"movq %rax, %rcx", "addq %rcx, %rax", "pushq %rax"},
				"scaled": {"imulq %rcx, %rax"},
				"masked": {"andq %rcx, %rax"},
				"less":   {"cmpq %rcx, %rax"},
			},
		},
		{
			name: "shift-count",
			src: `function shifted(x: i64): i64 { return x << 3i64; }
function shifted_wide(x: i64): i64 { return x >> 65i64; }
function main(): i32 { return (shifted(5i64) + shifted_wide(12i64)) as i32; }`,
			want: 40 + 6,
			has: map[string][]string{
				"shifted":      {"shlq $3, %rax"},
				"shifted_wide": {"sarq $1, %rax"},
			},
			lacks: map[string][]string{
				"shifted":      {"%cl", "movabsq"},
				"shifted_wide": {"%cl", "movabsq"},
			},
		},
		{
			// A literal past 32 bits has no immediate form at all, and the
			// movabsq that carries it is not a rename P5 takes either, so the
			// round trip survives. 2^31 fits the narrow constant form but not a
			// sign-extended imm32: P4 must refuse it, and P5 then materialises
			// it straight into %ecx.
			name: "refused-widths",
			src: `function wide(x: i64): i64 { return x + 4294967296i64; }
function narrow_big(x: i64): i64 { return x + 2147483648i64; }
function main(): i32 { return ((wide(1i64) + narrow_big(1i64)) % 100i64) as i32; }`,
			want: (4294967297 + 2147483649) % 100,
			has: map[string][]string{
				"wide":       {"movabsq $4294967296, %rax", "movq %rax, %rcx", "addq %rcx, %rax"},
				"narrow_big": {"movl $2147483648, %ecx", "addq %rcx, %rax"},
			},
			lacks: map[string][]string{
				"wide":       {"addq $4294967296"},
				"narrow_big": {"addq $2147483648", "movq %rax, %rcx"},
			},
		},
	})
}

// TestSelfHostTwoArgumentCallNeedsNoOperandStackX86_64 pins P5: a helper
// call whose second argument is a literal loads the first straight into its
// register and materialises the second into its own, with no push/pop pair
// around either — the operand stack was only protecting a value nothing
// disturbed. The string helpers' shape, which pushes both argument registers
// again before the call, folds the same way.
func TestSelfHostTwoArgumentCallNeedsNoOperandStackX86_64(t *testing.T) {
	runPeepholeFoldCases(t, []peepholeFoldCase{
		{
			name: "append-literal",
			src: `function grow(xs: i32[]): i32[] { return xs.append(7); }
function main(): i32 { var xs: i32[] = []; xs = grow(xs); xs = grow(xs); return xs[0] + xs[1] + xs.len(); }`,
			want: 16,
			has: map[string][]string{
				"grow": {"movq -8(%rbp), %rdi\n    movl $7, %esi\n    call __fern_arr_push"},
			},
			lacks: map[string][]string{
				"grow": {"movq %rax, %rsi", "popq %rdi"},
			},
		},
		{
			name: "string-literal-arg",
			src: `function is_ab(s: string): boolean { return s == "ab"; }
function main(): i32 { var n: i32 = 0; if (is_ab("ab")) { n = n + 1; } if (!is_ab("x")) { n = n + 2; } return n; }`,
			want: 3,
			has: map[string][]string{
				"is_ab": {"movq -8(%rbp), %rdi\n    leaq .SB0(%rip), %rsi\n    pushq %rsi\n    pushq %rdi\n    call __fn___fern_str_eq"},
			},
			lacks: map[string][]string{
				"is_ab": {"movq %rax, %rsi", "popq %rdi"},
			},
		},
	})
}

// TestSelfHostSingleArgumentLoadsStraightIntoItsRegisterX86_64 pins P6: a
// value computed into the accumulator for a register argument is materialised
// into that register instead, the copy being the last read of %rax before
// the call.
func TestSelfHostSingleArgumentLoadsStraightIntoItsRegisterX86_64(t *testing.T) {
	runPeepholeFoldCases(t, []peepholeFoldCase{
		{
			name: "field-into-rdi",
			src: `struct Rec { name: string }
function say(r: Rec): i32 { strbuf_append(r.name); return 1; }
function main(): i32 { strbuf_reset(); var r: Rec = Rec { name: "abc" }; var n: i32 = say(r); return n + strbuf_take().len(); }`,
			want: 4,
			has: map[string][]string{
				"say": {"movq 8(%rax), %rdi\n    call __fern_strbuf_append"},
			},
			lacks: map[string][]string{
				"say": {"movq %rax, %rdi"},
			},
		},
	})
}

// TestSelfHostConstZeroExtendedFormX86_64 pins the constant forms: a
// non-negative i32 literal is `movl $K, %eax` (five bytes, zero-extending),
// zero is the self-xor, a negative one keeps `movq` (whose imm32
// sign-extends), and an i64 literal only pays movabsq's ten bytes when it
// needs more than 32 bits. Each form is read back through a
// consumer that would expose the wrong extension.
func TestSelfHostConstZeroExtendedFormX86_64(t *testing.T) {
	runPeepholeFoldCases(t, []peepholeFoldCase{
		{
			name: "forms",
			src: `function pos(): i32 { return 7; }
function zero(): i32 { return 0; }
function neg(): i32 { return 0 - 3; }
function hex(): u32 { return 0xffffffff; }
function small64(): i64 { return 4294967295i64; }
function wide64(): i64 { return 4294967296i64; }
function main(): i32 {
    var h: u32 = hex();
    var w: i64 = wide64();
    var r: i32 = pos() + zero() + neg();
    if (h == 4294967295) { r = r + 10; }
    if (w == small64() + 1i64) { r = r + 100; }
    return r;
}`,
			want: 114,
			has: map[string][]string{
				"pos":     {"movl $7, %eax"},
				"zero":    {"xorl %eax, %eax"},
				"neg":     {"movq $-3, %rax"},
				"hex":     {"movl $0xffffffff, %eax"},
				"small64": {"movl $4294967295, %eax"},
				"wide64":  {"movabsq $4294967296, %rax"},
			},
			lacks: map[string][]string{
				"pos":     {"movq $7, %rax"},
				"zero":    {"movl $0, %eax", "movq $0, %rax"},
				"neg":     {"movl $-3, %eax"},
				"hex":     {"movq $0xffffffff"},
				"small64": {"movabsq"},
				"wide64":  {"movl"},
			},
		},
	})
}
