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

// CFI directives must wrap the prologue so libunwind / gdb can
// reconstruct the caller's frame.
func TestCFIDirectivesPresent(t *testing.T) {
	asm := compile(t, `function f(): number { return 0; }`)
	mustContain(t, asm, ".cfi_startproc")
	mustContain(t, asm, ".cfi_def_cfa_offset 8")
	mustContain(t, asm, ".cfi_offset fp, -8")
	mustContain(t, asm, ".cfi_offset lr, -4")
	mustContain(t, asm, ".cfi_def_cfa_register fp")
	mustContain(t, asm, ".cfi_endproc")
}

// Leaf functions push extra callee-saved registers, so each gets its
// own .cfi_offset entry.
func TestCFIDirectivesCoverLeafSavedRegisters(t *testing.T) {
	asm := compile(t, `function add(a: number, b: number): number { return a + b; }`)
	// (P+2)*4 = (2+2)*4 = 16
	mustContain(t, asm, ".cfi_def_cfa_offset 16")
	mustContain(t, asm, ".cfi_offset r4, -16")
	mustContain(t, asm, ".cfi_offset r5, -12")
	mustContain(t, asm, ".cfi_offset fp, -8")
	mustContain(t, asm, ".cfi_offset lr, -4")
}

func TestArithmetic(t *testing.T) {
	// Constant folding would collapse literal arithmetic before
	// emit, so use parameters to keep the binop visible.
	asm := compile(t, `function f(a: number, b: number): number { return a + b; }`)
	mustContain(t, asm, "add r0, r1, r0")
}

func TestSubtractionOrder(t *testing.T) {
	// Left operand must end up in r1, right in r0; sub must be `r1 - r0`.
	asm := compile(t, `function f(a: number, b: number): number { return a - b; }`)
	mustContain(t, asm, "sub r0, r1, r0")
}

func TestComparisonEmitsCondMoves(t *testing.T) {
	asm := compile(t, `function f(a: number, b: number): boolean { return a < b; }`)
	mustContain(t, asm, "movlt r0, #1")
	mustContain(t, asm, "movge r0, #0")
}

// `a && b` lowers to `if a then b else 0`; the IR emits it as an
// OpIf, which the backend translates into `cmp r0, #0; beq <else>`.
func TestShortCircuitAnd(t *testing.T) {
	asm := compile(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	mustContain(t, asm, "cmp r0, #0")
	mustContain(t, asm, "beq .LifElse_")
}

func TestShortCircuitOr(t *testing.T) {
	asm := compile(t, `function f(a: boolean, b: boolean): boolean { return a || b; }`)
	// `a || b` is `if a then 1 else b` — same `beq` / OpIf shape, with
	// the constant-1 branch sitting in the `then` arm.
	mustContain(t, asm, "cmp r0, #0")
	mustContain(t, asm, "beq .LifElse_")
}

func TestCallEmitsBl(t *testing.T) {
	// Use a callee with an if-statement so the IR inliner skips it
	// — otherwise the `bl g` we're looking for gets substituted away.
	asm := compile(t, `function g(n: number): number {
		if (n == 0) { return 1; }
		return n + 1;
	}
function f(): number { return g(5); }`)
	mustContain(t, asm, "bl g")
}

func TestArrayIndex(t *testing.T) {
	asm := compile(t, `function f(): number { var a: number[] = [1,2,3]; return a[1]; }`)
	// Indexing computes `base + idx*4` then dereferences. The
	// IR-driven path emits the address calc and load as separate
	// instructions instead of the AST walker's single
	// `ldr r0, [r1, r0, lsl #2]`.
	mustContain(t, asm, "add r0, r1, r0, lsl #2")
	mustContain(t, asm, "ldr r0, [r0]")
	mustContain(t, asm, "bl __lang_alloc")
	mustContain(t, asm, ".global __lang_alloc")
}

// Arrays carry a 4-byte little-endian length prefix at base-4. The
// IR lowers `len(x)` to `<expr>; const 4; sub; load`, which on arm32
// becomes a `sub r0, r1, r0` (ptr - 4) followed by `ldr r0, [r0]` —
// no libc call, just two instructions instead of the AST walker's
// fused `ldr r0, [r0, #-4]`.
func TestLenOfArrayLoadsPrefix(t *testing.T) {
	asm := compile(t, `function f(): number {
		var a: number[] = [10, 20, 30];
		return len(a);
	}`)
	mustContain(t, asm, "sub r0, r1, r0")
	mustContain(t, asm, "ldr r0, [r0]")
	if strings.Contains(asm, "bl strlen") {
		t.Errorf("len(array) must not call strlen:\n%s", asm)
	}
}

// The malloc wrapper appears once and only when something in the
// program asks for heap storage. A pure-arithmetic function
// shouldn't pull in the runtime.
func TestAllocRuntimeOnlyEmittedWhenNeeded(t *testing.T) {
	asm := compile(t, `function f(): number { return 1 + 2; }`)
	if strings.Contains(asm, "__lang_alloc") {
		t.Errorf("alloc helper should not appear in pure-arith program:\n%s", asm)
	}
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

// >4-arg calls push each arg onto the IR's operand stack in source
// order, then load r0..r3 from the appropriate offsets and reverse
// the extras into AAPCS layout (leftmost-stack-arg at sp+0).
func TestManyArgCallPreallocates(t *testing.T) {
	// Branchy callee to keep the IR inliner from substituting it
	// away — we want an actual `bl g` so we can inspect the
	// arg-passing dance.
	asm := compile(t, `
		function g(a: number, b: number, c: number, d: number, e: number, f: number): number {
			if (a == 0) { return 0; }
			return a;
		}
		function f(): number { return g(1, 2, 3, 4, 5, 6); }`)
	// Args 0..3 read from their pushed offsets.
	mustContain(t, asm, "ldr r0, [sp, #20]")
	mustContain(t, asm, "ldr r1, [sp, #16]")
	mustContain(t, asm, "ldr r2, [sp, #12]")
	mustContain(t, asm, "ldr r3, [sp, #8]")
	// Inner-arg slots get reclaimed; extras stay on stack.
	mustContain(t, asm, "add sp, sp, #16")
	// After the call, the K extras (2 * 4 bytes here) are popped.
	mustContain(t, asm, "add sp, sp, #8")
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
	asm := compile(t, `function f(a: number, b: number): number { return a & b; }`)
	mustContain(t, asm, "and r0, r1, r0")
}

func TestBitwiseOrXor(t *testing.T) {
	asm := compile(t, `function f(a: number, b: number, c: number): number { return a | b ^ c; }`)
	mustContain(t, asm, "orr r0, r1, r0")
	mustContain(t, asm, "eor r0, r1, r0")
}

func TestShiftLeft(t *testing.T) {
	asm := compile(t, `function f(a: number, b: number): number { return a << b; }`)
	mustContain(t, asm, "lsl r0, r1, r0")
}

func TestShiftRight(t *testing.T) {
	asm := compile(t, `function f(a: number, b: number): number { return a >> b; }`)
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

// The leaf epilogue must step sp back to where r4 was pushed (fp -
// 4*P) so the `pop {r4..r{4+P-1}, fp, lr}` reads the saved
// registers in the right order. A naïve `mov sp, fp` lands sp at
// the saved-fp word and the pop reads `r4 = saved_fp`, scrambling
// the callee-saved set and lr — verified by qemu-arm e2e tests
// which exit -1 / return wrong values without this offset.
func TestLeafEpilogueStepsSPPastSavedRegs(t *testing.T) {
	asm := compile(t, `function add(a: number, b: number): number { return a + b; }`)
	// 2 params → fp - 8 lands sp at saved r4.
	mustContain(t, asm, "sub sp, fp, #8")
	mustContain(t, asm, "pop {r4, r5, fp, lr}")
	if strings.Contains(asm, "mov sp, fp\n\tpop {r4") {
		t.Errorf("leaf epilogue must NOT use `mov sp, fp` before popping savees:\n%s", asm)
	}
}

// Same check at 4 params: epilogue offset scales with the param
// count (4 saved regs × 4 = 16).
func TestLeafEpilogueOffsetScalesWithParams(t *testing.T) {
	asm := compile(t, `function leaf(a: number, b: number, c: number, d: number): number {
		return (a + b) * (c + d);
	}`)
	mustContain(t, asm, "sub sp, fp, #16")
	mustContain(t, asm, "pop {r4, r5, r6, r7, fp, lr}")
}

// Calling a function value held in a `var` lowers to `blx r12`
// after loading the function pointer. ConstPropagate inlines the
// function reference itself when the var is bound to a known
// function name, so the symbol shows up directly via `ldr r0,
// =<name>` rather than going through the slot. To keep the test
// honest about the indirect-dispatch path, the function value
// comes through a parameter (which the propagator can't reason
// about).
func TestIndirectCallThroughVar(t *testing.T) {
	asm := compile(t, `function add(a: number, b: number): number { return a + b; }
function call_with(f: (number, number) => number): number {
	return f(40, 2);
}
function main(): number { return call_with(add); }`)
	mustContain(t, asm, "blx r12")
}

// `return self(args)` is rewritten by ir.TailCallOptimize into a
// parameter rebind plus a backward `OpBr` to a synthetic loop
// wrapping the body. On arm32 that materialises as a `b .LloopTop_*`
// — no `bl <self>` for the recursive call.
func TestTailRecursionBranchesToBody(t *testing.T) {
	asm := compile(t, `function sum(n: number, acc: number): number {
		if (n == 0) { return acc; }
		return sum(n - 1, acc + n);
	}`)
	mustContain(t, asm, ".LloopTop_")
	mustContain(t, asm, "b .LloopTop_")
	if strings.Contains(asm, "bl sum") {
		t.Errorf("tail call should not emit `bl sum`:\n%s", asm)
	}
}

// Non-tail recursion (the recursive call isn't the return value) must
// still emit a regular `bl` so register state is preserved across it.
func TestNonTailRecursionKeepsBl(t *testing.T) {
	asm := compile(t, `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}`)
	mustContain(t, asm, "bl fact")
}

// Functions that contain a Call expression are NOT leaves — the
// existing stack-spill prologue still applies.
func TestNonLeafKeepsStackSpill(t *testing.T) {
	// Branchy callee resists IR inlining; `f` then keeps the
	// non-leaf shape because it makes a real `bl g` call.
	asm := compile(t, `function g(n: number): number {
		if (n == 0) { return 0; }
		return n;
	}
function f(n: number): number { return g(n); }`)
	// `f` calls `g`, so it isn't a leaf — expect the original prologue
	// (CFI directives may be interleaved between the label and the push).
	if !strings.Contains(asm, "f:\n\t.cfi_startproc\n\tpush {fp, lr}") {
		t.Errorf("non-leaf f should use the stack-spill prologue:\n%s", asm)
	}
}

// EmitWithOptions(SourceFile: …) should declare the source file and
// emit `.loc 1 <line> <col>` before each statement so `gcc -g` can
// build a DWARF line-number table.
func TestDwarfLocDirectives(t *testing.T) {
	src := `function f(): number {
	var x = 1;
	return x;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	asm, err := EmitWithOptions(prog, info, Options{SourceFile: "f.lang"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, asm, `.file 1 "f.lang"`)
	mustContain(t, asm, ".loc 1 2") // var x = 1;
	mustContain(t, asm, ".loc 1 3") // return x;
}

// Plain Emit (no Options) should not emit any debug directives — the
// CI smoke output and existing tests must stay byte-identical.
func TestNoDebugDirectivesByDefault(t *testing.T) {
	asm := compile(t, `function f(): number { return 0; }`)
	if strings.Contains(asm, ".file") || strings.Contains(asm, ".loc") {
		t.Errorf("default Emit should not emit debug directives:\n%s", asm)
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

func TestArm32RejectsFloatWithClearError(t *testing.T) {
	prog, err := parser.Parse(`function f(): float { return 1.5; }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Emit(prog, info); err == nil {
		t.Fatal("expected error from arm32 backend on float program")
	} else if !strings.Contains(err.Error(), "float") {
		t.Errorf("error should mention float, got %v", err)
	}
}

// switch lowers to a chain of nested blocks: each case opens an
// outer "skip-on-no-match" + inner "match-target" block, with
// br_if on each value comparison. From the assembly side it
// shows up as a swarm of blkEnd labels and `bne`/`beq` jumps.
func TestArm32SwitchEmitsBranchChain(t *testing.T) {
	asm := compile(t, `function f(n: number): number {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
		return -1;
	}`)
	mustContain(t, asm, ".LblkEnd_")
	mustContain(t, asm, "cmp r1, r0")
	mustContain(t, asm, "moveq r0, #1")
	if !strings.Contains(asm, "bne") && !strings.Contains(asm, "beq") {
		t.Errorf("expected switch dispatch to use conditional branches:\n%s", asm)
	}
}

// Ternary is just a typed `if/else` in IR (block-result i32), so
// the assembly is a `beq <else>` + a fall-through `b <end>`.
func TestArm32TernaryBranches(t *testing.T) {
	asm := compile(t, `function f(b: boolean): number { return b ? 1 : 2; }`)
	mustContain(t, asm, ".LifElse_")
	mustContain(t, asm, ".LifEnd_")
	mustContain(t, asm, "beq")
}

func TestArm32CompoundAssignLowersToBinary(t *testing.T) {
	asm := compile(t, `function f(): number { var x: number = 5; x += 7; return x; }`)
	// 7 should appear as an immediate from the binary lowering.
	mustContain(t, asm, "=7")
}

func TestArm32StringIndexLoadsByte(t *testing.T) {
	asm := compile(t, `function f(): number { var s: string = "abc"; return s[1]; }`)
	mustContain(t, asm, "ldrb")
}

func TestArm32StringEqualityCallsStrcmp(t *testing.T) {
	asm := compile(t, `function f(): boolean { return "a" == "a"; }`)
	mustContain(t, asm, "bl strcmp")
}

// `len(string)` no longer calls strlen — strings carry the same
// 4-byte length prefix as arrays, so the lowering threads through
// the same `<ptr>; const 4; sub; load` IR sequence.
func TestArm32LenStringLoadsPrefix(t *testing.T) {
	asm := compile(t, `function f(): number { return len("abc"); }`)
	mustContain(t, asm, "sub r0, r1, r0")
	mustContain(t, asm, "ldr r0, [r0]")
	if strings.Contains(asm, "bl strlen") {
		t.Errorf("len(string) must not call strlen:\n%s", asm)
	}
}

// String literals in .rodata are emitted with a 4-byte length prefix
// immediately before the labelled data, with .align 2 ahead of each
// one so consecutive odd-length strings stay word-aligned.
func TestArm32StringLiteralHasLengthPrefix(t *testing.T) {
	asm := compile(t, `function f(): string { return "hi"; }`)
	mustContain(t, asm, ".align 2")
	mustContain(t, asm, ".4byte 2")
	// The data label still owns `.asciz "hi"`; the prefix sits just
	// above it.
	mustContain(t, asm, `.asciz "hi"`)
}

// String concat uses the prefixed buffer directly: read the lengths
// from the operands' prefixes (no strlen), allocate via __lang_alloc,
// write the new prefix, and copy the bytes through.
func TestArm32StrcatReadsPrefixesAndAllocates(t *testing.T) {
	asm := compile(t, `function f(): string { return "a" + "b"; }`)
	mustContain(t, asm, "bl __lang_strcat")
	mustContain(t, asm, "bl __lang_alloc")
	// Body of __lang_strcat: prefix-loads of both operands, the
	// alloc, the prefix store, and the two memcpy calls.
	mustContain(t, asm, "ldr r6, [r0, #-4]")
	mustContain(t, asm, "ldr r7, [r1, #-4]")
	mustContain(t, asm, "str r1, [r0, #-4]")
	if strings.Contains(asm, "bl strlen") {
		t.Errorf("strcat must not call strlen — lengths come from the prefix:\n%s", asm)
	}
}
