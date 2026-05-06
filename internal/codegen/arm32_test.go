package codegen

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

func compile(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

func mustContain(t *testing.T, asm, needle string) {
	t.Helper()
	if !strings.Contains(asm, needle) {
		t.Errorf("expected output to contain %q\n--- output ---\n%s", needle, asm)
	}
}

func TestPrologueEpilogue(t *testing.T) {
	asm := compile(t, `function f(): number { return 0; }`)
	mustContain(t, asm, "push {fp, lr}")
	mustContain(t, asm, "mov fp, sp")
	mustContain(t, asm, "pop {fp, lr}")
	mustContain(t, asm, "bx lr")
	mustContain(t, asm, ".global f")
}

func TestArithmetic(t *testing.T) {
	asm := compile(t, `function f(): number { return 1 + 2; }`)
	mustContain(t, asm, "add r0, r1, r0")
}

func TestSubtractionOrder(t *testing.T) {
	// Left operand must end up in r1, right in r0; sub must be `r1 - r0`.
	asm := compile(t, `function f(): number { return 10 - 3; }`)
	mustContain(t, asm, "sub r0, r1, r0")
}

func TestComparisonEmitsCondMoves(t *testing.T) {
	asm := compile(t, `function f(): boolean { return 1 < 2; }`)
	mustContain(t, asm, "movlt r0, #1")
	mustContain(t, asm, "movge r0, #0")
}

func TestShortCircuitAnd(t *testing.T) {
	asm := compile(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	// Left value evaluated, then if zero we branch over the right side.
	mustContain(t, asm, "beq .Lsc_")
}

func TestShortCircuitOr(t *testing.T) {
	asm := compile(t, `function f(a: boolean, b: boolean): boolean { return a || b; }`)
	mustContain(t, asm, "bne .Lsc_")
}

func TestCallEmitsBl(t *testing.T) {
	asm := compile(t, `function g(n: number): number { return n + 1; }
function f(): number { return g(5); }`)
	mustContain(t, asm, "bl g")
}

func TestArrayIndex(t *testing.T) {
	asm := compile(t, `function f(): number { var a: number[] = [1,2,3]; return a[1]; }`)
	mustContain(t, asm, "ldr r0, [r1, r0, lsl #2]")
}

func TestAssignment(t *testing.T) {
	asm := compile(t, `function f(): number { var x = 0; x = 5; return x; }`)
	// x at fp-4: store of 5 must hit that slot.
	mustContain(t, asm, "str r0, [fp, #-4]")
}

func TestFrameSizeAlignedTo8(t *testing.T) {
	// Three locals: 12 bytes, must round to 16.
	asm := compile(t, `function f(): number { var a = 1; var b = 2; var c = 3; return a + b + c; }`)
	mustContain(t, asm, "sub sp, sp, #16")
}

// A function with >4 params should fetch the extras from the caller's
// stack frame (fp+8, fp+12, …) into local slots.
func TestManyParamsReadsFromCallerStack(t *testing.T) {
	asm := compile(t, `function f(a: number, b: number, c: number, d: number, e: number, f2: number): number {
		return a + b + c + d + e + f2;
	}`)
	mustContain(t, asm, "ldr r12, [fp, #8]")  // param 4 (e)
	mustContain(t, asm, "ldr r12, [fp, #12]") // param 5 (f2)
}

// >4-arg calls must pre-allocate the AAPCS stack-arg area and load
// r0..r3 from the temp staging slots.
func TestManyArgCallPreallocates(t *testing.T) {
	asm := compile(t, `
		function g(a: number, b: number, c: number, d: number, e: number, f: number): number { return a; }
		function f(): number { return g(1, 2, 3, 4, 5, 6); }`)
	// 6 args * 4 bytes = 24, already 8-aligned.
	mustContain(t, asm, "sub sp, sp, #24")
	mustContain(t, asm, "add sp, sp, #24")
}

func TestNonExecutableStackNote(t *testing.T) {
	asm := compile(t, `function f(): number { return 0; }`)
	mustContain(t, asm, `.section .note.GNU-stack,"",%progbits`)
}

// String literals are interned in .rodata under unique labels and
// loaded by address into r0.
func TestStringLiteralEmitsRodata(t *testing.T) {
	asm := compile(t, `function main(): void { print("hi"); }`)
	mustContain(t, asm, ".section .rodata")
	mustContain(t, asm, ".LStr_0:")
	mustContain(t, asm, `.asciz "hi"`)
	mustContain(t, asm, "ldr r0, =.LStr_0")
}

// `print(s)` lowers to a direct call to libc puts.
func TestPrintLowersToPuts(t *testing.T) {
	asm := compile(t, `function main(): void { print("hi"); }`)
	mustContain(t, asm, "bl puts")
}

// Identical literals share a label; distinct ones don't.
func TestModuloUsesIdivmod(t *testing.T) {
	asm := compile(t, `function f(): number { return 17 % 5; }`)
	mustContain(t, asm, "bl __aeabi_idivmod")
	mustContain(t, asm, "mov r0, r1")
}

func TestBitwiseAnd(t *testing.T) {
	asm := compile(t, `function f(): number { return 12 & 10; }`)
	mustContain(t, asm, "and r0, r1, r0")
}

func TestBitwiseOrXor(t *testing.T) {
	asm := compile(t, `function f(): number { return 1 | 2 ^ 4; }`)
	mustContain(t, asm, "orr r0, r1, r0")
	mustContain(t, asm, "eor r0, r1, r0")
}

func TestShiftLeft(t *testing.T) {
	asm := compile(t, `function f(): number { return 1 << 3; }`)
	mustContain(t, asm, "lsl r0, r1, r0")
}

func TestShiftRight(t *testing.T) {
	asm := compile(t, `function f(): number { return 16 >> 2; }`)
	mustContain(t, asm, "asr r0, r1, r0")
}

// `string + string` should lower to a runtime call, and the helper
// must be emitted exactly once at the end of the .text section.
func TestStringConcatLowersToRuntime(t *testing.T) {
	asm := compile(t, `function main(): void { print("a" + "b"); }`)
	mustContain(t, asm, "bl __lang_strcat")
	mustContain(t, asm, ".global __lang_strcat")
	if strings.Count(asm, ".global __lang_strcat") != 1 {
		t.Errorf("__lang_strcat helper emitted more than once")
	}
}

// Programs without string `+` should NOT pull in the helper.
func TestNoStrcatHelperWhenUnused(t *testing.T) {
	asm := compile(t, `function main(): void { print("hello"); }`)
	if strings.Contains(asm, "__lang_strcat") {
		t.Errorf("strcat helper emitted even though it's unused:\n%s", asm)
	}
}

// Leaf functions (no calls in the body) should pin their parameters
// to callee-saved registers instead of stack slots. The prologue
// pushes r4..r{4+P-1} alongside fp/lr and reads through `mov r0, r4`
// rather than `ldr r0, [fp, ...]`.
func TestLeafFunctionPinsParamsToRegisters(t *testing.T) {
	asm := compile(t, `function add(a: number, b: number): number { return a + b; }`)
	mustContain(t, asm, "push {r4, r5, fp, lr}")
	mustContain(t, asm, "mov r4, r0")
	mustContain(t, asm, "mov r5, r1")
	// Reads of `a` go through r4, not a stack load.
	mustContain(t, asm, "mov r0, r4")
	if strings.Contains(asm, "str r0, [fp, #-4]") {
		t.Errorf("leaf function should not spill params to stack:\n%s", asm)
	}
}

// Functions that contain a Call expression are NOT leaves — the
// existing stack-spill prologue still applies.
func TestNonLeafKeepsStackSpill(t *testing.T) {
	asm := compile(t, `function g(n: number): number { return n; }
function f(n: number): number { return g(n); }`)
	// `f` calls `g`, so it isn't a leaf — expect the original prologue.
	if !strings.Contains(asm, "f:\n\tpush {fp, lr}") {
		t.Errorf("non-leaf f should use the stack-spill prologue:\n%s", asm)
	}
}

func TestStringInterningDeduplicates(t *testing.T) {
	asm := compile(t, `function main(): void { print("a"); print("a"); print("b"); }`)
	mustContain(t, asm, ".LStr_0:")
	mustContain(t, asm, ".LStr_1:")
	if strings.Contains(asm, ".LStr_2:") {
		t.Errorf("third label should not exist (dedup), got:\n%s", asm)
	}
}
