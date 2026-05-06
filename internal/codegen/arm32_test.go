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
