package codegen

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// emitIR is the test-side helper that mirrors compile() but routes
// through EmitFromIR so the IR-driven backend gets exercised.
func emitIR(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := EmitFromIR(prog, info, Options{})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

// Smallest possible function — no params, no locals, no ops beyond
// the implicit return. The IR-driven emitter has to produce the same
// outer scaffolding (.global / prologue / .cfi_* / epilogue / .size)
// the AST walker does.
func TestIRArm32EmitsModuleSkeleton(t *testing.T) {
	asm := emitIR(t, `function main(): number { return 0; }`)
	mustContain(t, asm, ".arch armv7-a")
	mustContain(t, asm, ".global main")
	mustContain(t, asm, ".type main, %function")
	mustContain(t, asm, "main:")
	mustContain(t, asm, ".cfi_startproc")
	mustContain(t, asm, ".cfi_endproc")
	mustContain(t, asm, ".size main, .-main")
	mustContain(t, asm, `.section .note.GNU-stack,"",%progbits`)
}

// Arithmetic + locals exercise the stack-machine translation. The
// peephole pass folds adjacent push/pop pairs out, but the binop
// shape (add of two operands using r0/r1) survives.
func TestIRArm32ArithmeticAndLocals(t *testing.T) {
	asm := emitIR(t, `function f(): number {
		var x: number = 7;
		var y: number = x + 3;
		return y;
	}`)
	mustContain(t, asm, "ldr r0, =7")
	mustContain(t, asm, "ldr r0, =3")
	mustContain(t, asm, "add r0, r1, r0")
	// Locals occupy fp-relative slots.
	mustContain(t, asm, "str r0, [fp, #-4]")
	mustContain(t, asm, "str r0, [fp, #-8]")
}

// Comparisons normalise to 0 / 1 via paired conditional moves.
func TestIRArm32ComparisonNormalises(t *testing.T) {
	asm := emitIR(t, `function f(a: number, b: number): boolean { return a < b; }`)
	mustContain(t, asm, "cmp r1, r0")
	mustContain(t, asm, "movlt r0, #1")
	mustContain(t, asm, "movge r0, #0")
}

// `if/else` lowers to OpIf / OpElse / OpEnd. The emitter wires a
// `beq <else>` branch into the if header; OpElse emits a `b <end>`
// to skip past the else label.
func TestIRArm32IfElse(t *testing.T) {
	asm := emitIR(t, `function f(n: number): number {
		if (n == 0) { return 1; }
		return 2;
	}`)
	mustContain(t, asm, "cmp r0, #0")
	mustContain(t, asm, "beq")
	// The early return inside the if-body branches to the epilogue;
	// the peephole optimiser folds the trailing return's `b` into
	// fall-through, so we only see one `b .Lepi_` in the cleaned-up
	// output.
	if !strings.Contains(asm, "b .Lepi_") {
		t.Errorf("expected an early `b .Lepi_` from the if-branch return:\n%s", asm)
	}
}

// while expands to a wrap `block` + an inner `loop`. The TCO pass
// runs first but does nothing for non-recursive functions, so the
// shape is the canonical br + br_if pair.
func TestIRArm32WhileEmitsLoop(t *testing.T) {
	asm := emitIR(t, `function f(): number {
		var i: number = 0;
		while (i < 10) { i = i + 1; }
		return i;
	}`)
	if !strings.Contains(asm, ".LloopTop_") {
		t.Errorf("expected a loopTop_ label:\n%s", asm)
	}
	if !strings.Contains(asm, ".LblkEnd_") {
		t.Errorf("expected a blkEnd_ label:\n%s", asm)
	}
}

// A direct call lowers to `bl <name>`. Args were pushed l→r so we
// pop them in reverse into r0..r3 before the branch.
func TestIRArm32DirectCall(t *testing.T) {
	asm := emitIR(t, `function add(a: number, b: number): number { return a + b; }
		function main(): number { return add(2, 3); }`)
	mustContain(t, asm, "bl add")
	// b's index is 1 → it should be popped into r1 first (since it's
	// pushed last and sits on top of stack); a goes into r0.
	mustContain(t, asm, "pop {r1}")
	mustContain(t, asm, "pop {r0}")
}

// Self-recursive tail call: TCO rewrites it into a parameter rebind
// + a backward br to the wrapping loop. The emitted asm has no
// `bl <self>`; the loop top label is what the back-edge targets.
func TestIRArm32TailCallBecomesLoop(t *testing.T) {
	asm := emitIR(t, `function fact(n: number, acc: number): number {
		if (n == 0) { return acc; }
		return fact(n - 1, acc * n);
	}`)
	if strings.Contains(asm, "bl fact") {
		t.Errorf("self-tail call must not emit `bl fact`:\n%s", asm)
	}
	if !strings.Contains(asm, ".LloopTop_") {
		t.Errorf("expected a loopTop_ label from the TCO wrap:\n%s", asm)
	}
}

// `__lang_alloc` must come along when arrays are used; the helper is
// emitted only once per module and indices land at base+4 (post-prefix).
func TestIRArm32ArrayAllocates(t *testing.T) {
	asm := emitIR(t, `function f(): number {
		var a: number[] = [10, 20, 30];
		return a[1];
	}`)
	mustContain(t, asm, "bl __lang_alloc")
	mustContain(t, asm, ".global __lang_alloc")
}

// String concatenation goes through `__lang_strcat`; that helper
// itself bottoms out in `__lang_alloc`, so usesAlloc gets pulled in
// transitively.
func TestIRArm32StringConcatRunsThroughHelpers(t *testing.T) {
	asm := emitIR(t, `function f(): string { return "a" + "b"; }`)
	mustContain(t, asm, "bl __lang_strcat")
	mustContain(t, asm, ".global __lang_strcat")
	mustContain(t, asm, ".global __lang_alloc")
}

// Float ops still aren't supported on the arm32 backend — same
// behaviour as the AST walker, so existing programs continue to
// reject the same way.
func TestIRArm32RejectsFloats(t *testing.T) {
	prog, err := parser.Parse(`function f(): float { return 1.5 + 2.5; }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EmitFromIR(prog, info, Options{}); err == nil {
		t.Fatal("expected error on float program")
	}
}

// DWARF .loc directives appear when SourceFile is set, with one .loc
// per source line crossing.
func TestIRArm32EmitsLocDirectivesWithSourceFile(t *testing.T) {
	prog, err := parser.Parse(`function f(): number {
		var x: number = 1;
		return x;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	asm, err := EmitFromIR(prog, info, Options{SourceFile: "test.lang"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, asm, `.file 1 "test.lang"`)
	mustContain(t, asm, ".loc 1 2")
	mustContain(t, asm, ".loc 1 3")
}
