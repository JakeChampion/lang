package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// The emitter-shape gates for asm_ir.fern's register path on constants: the
// immediate operand forms and the constant materialisation forms — the
// self-host mirror of native's const_alu_fold_test.go and
// const_zero_xor_test.go (internal/codegen/x86_64).
//
// Each case asserts two things that have to travel together: the selected
// form is present in the function that produces it and the form it replaces
// is absent, AND the program still exits with the value the interpreter gives
// it. Either alone is easy to satisfy wrongly.
//
// The shapes are asserted per FUNCTION BODY rather than over the whole module,
// because the hand-written runtime carries its own copies of every mnemonic
// here. The allocator picks the register, so each line is a pattern with the
// register left open, and a back-reference where two operands must agree.

// x86ShapeHarness builds the asm_ir_run driver once and returns an emit
// function (source -> asm text) and a run function (asm -> exit code).
func x86ShapeHarness(t *testing.T) (func(t *testing.T, src string) string, func(t *testing.T, name, asm string) int) {
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

// shapeFnBody returns the emitted body of `__fn_<name>`, up to its `ret`.
func shapeFnBody(t *testing.T, asm, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)\n__fn_` + regexp.QuoteMeta(name) + `:\n(.*?)\n    ret\n`)
	m := re.FindStringSubmatch(asm)
	if m == nil {
		t.Fatalf("__fn_%s not found in emitted asm:\n%s", name, asm)
	}
	return m[1]
}

func runX86ShapeCases(t *testing.T, cases []shapeCase) {
	t.Helper()
	emit, run := x86ShapeHarness(t)
	runShapeCases(t, emit, run, cases)
}

// TestSelfHostConstOperandReachesImmediateFormX86_64 pins the immediate
// operand: a binary operation against a literal reads it as an immediate and
// never materialises it in a register first. The two-operand imulq has no
// immediate form, so it takes the three-operand one; a shift count folds to
// the imm8 form, reduced modulo the width.
//
// Width is the rule's whole risk, so the refusals are pinned too: a literal
// past a sign-extended imm32 cannot be an immediate, and must still compute
// the right answer through a register, materialised in the narrowest form
// that holds it (2^31 is the boundary: it fits `movl` but not the imm32).
func TestSelfHostConstOperandReachesImmediateFormX86_64(t *testing.T) {
	runX86ShapeCases(t, []shapeCase{
		{
			name: "alu",
			src: `@noinline function bump(x: i64): i64 { return x + 1i64; }
@noinline function scaled(x: i64): i64 { return x * 10i64; }
@noinline function masked(x: i32): i32 { return x & 6; }
@noinline function less(x: i32): boolean { return x < 7; }
function main(): i32 {
    var i: i64 = 0i64; var s: i64 = 0i64;
    while (i < 3i64) { s = s + bump(i) + scaled(i) + (masked(i as i32) as i64); if (less(i as i32)) { s = s + 100i64; } i = i + 1i64; }
    return (s % 100i64) as i32;
}`,
			want: (300 + (1 + 0 + 0) + (2 + 10 + 0) + (3 + 20 + 2)) % 100,
			has: map[string][]string{
				"bump":   {`\n    addq \$1, %r[a-z0-9]+\n`},
				"scaled": {`\n    imulq \$10, (%r[a-z0-9]+), \1\n`},
				"masked": {`\n    andq \$6, %r[a-z0-9]+\n`},
				"less":   {`\n    cmpq \$7, %r[a-z0-9]+\n`},
			},
			lacks: map[string][]string{
				"bump":   {`\$1, %e`, `addq %r[a-z0-9]+, %r`, `popq`},
				"scaled": {`\$10, %e`, `imulq %r[a-z0-9]+, %r`},
				"masked": {`\$6, %e`, `andq %r[a-z0-9]+, %r`},
				"less":   {`\$7, %e`, `cmpq %r[a-z0-9]+, %r`},
			},
		},
		{
			name: "shift-count",
			src: `@noinline function shifted(x: i64): i64 { return x << 3i64; }
@noinline function shifted_wide(x: i64): i64 { return x >> 65i64; }
function main(): i32 { return (shifted(5i64) + shifted_wide(12i64)) as i32; }`,
			want: 40 + 6,
			has: map[string][]string{
				"shifted":      {`\n    shlq \$3, %r[a-z0-9]+\n`},
				"shifted_wide": {`\n    sarq \$1, %r[a-z0-9]+\n`},
			},
			lacks: map[string][]string{
				"shifted":      {`%cl`, `movabsq`},
				"shifted_wide": {`%cl`, `movabsq`},
			},
		},
		{
			name: "refused-widths",
			src: `@noinline function wide(x: i64): i64 { return x + 4294967296i64; }
@noinline function narrow_big(x: i64): i64 { return x + 2147483648i64; }
function main(): i32 { return ((wide(1i64) + narrow_big(1i64)) % 100i64) as i32; }`,
			want: (4294967297 + 2147483649) % 100,
			has: map[string][]string{
				"wide":       {`\n    movabsq \$4294967296, %(r[a-z0-9]+)\n    addq %\1, %r[a-z0-9]+\n`},
				"narrow_big": {`\n    movl \$2147483648, %e([a-z0-9]+)\n    addq %r\1, %r[a-z0-9]+\n`},
			},
			lacks: map[string][]string{
				"wide":       {`addq \$4294967296`},
				"narrow_big": {`addq \$2147483648`, `movq \$2147483648`, `movabsq`},
			},
		},
	})
}

// TestSelfHostConstZeroExtendedFormX86_64 pins the constant forms: a
// non-negative literal under 2^32 is `movl $K, %e..` (five bytes,
// zero-extending), zero is the self-xor, a negative one keeps `movq` (whose
// imm32 sign-extends), and an i64 literal only pays movabsq's ten bytes when
// it needs more than 32 bits. Each form is read back through a consumer that
// would expose the wrong extension.
func TestSelfHostConstZeroExtendedFormX86_64(t *testing.T) {
	runX86ShapeCases(t, []shapeCase{
		{
			name: "forms",
			src: `@noinline function pos(): i32 { return 7; }
@noinline function zero(): i32 { return 0; }
@noinline function neg(): i32 { return 0 - 3; }
@noinline function hex(): u32 { return 0xffffffff; }
@noinline function small64(): i64 { return 4294967295i64; }
@noinline function wide64(): i64 { return 4294967296i64; }
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
				"pos":     {`\n    movl \$7, %e[a-z0-9]+\n`},
				"zero":    {`\n    xorl %(e[a-z0-9]+), %\1\n`},
				"neg":     {`\n    movq \$-3, %r[a-z0-9]+\n`},
				"hex":     {`\n    movl \$0xffffffff, %e[a-z0-9]+\n`},
				"small64": {`\n    movl \$4294967295, %e[a-z0-9]+\n`},
				"wide64":  {`\n    movabsq \$4294967296, %r[a-z0-9]+\n`},
			},
			lacks: map[string][]string{
				"pos":     {`movq \$7`},
				"zero":    {`movl \$0,`, `movq \$0,`},
				"neg":     {`movl \$-3`},
				"hex":     {`movq \$0xffffffff`},
				"small64": {`movabsq`, `movq \$4294967295`},
				"wide64":  {`movl`},
			},
		},
	})
}
