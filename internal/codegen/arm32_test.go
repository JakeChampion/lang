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

// compileDebug is the debug-mode counterpart of `compile`:
// passes a non-empty SourceFile so codegen emits .file / .loc /
// .cfi_* directives. Used by tests that pin debug-only output.
func compileDebug(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := EmitWithOptions(prog, info, Options{SourceFile: "f.lang"})
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
// reconstruct the caller's frame — but only in debug builds
// (SourceFile != ""). Release binaries skip them to save the
// .eh_frame section weight.
func TestCFIDirectivesPresentInDebugMode(t *testing.T) {
	asm := compileDebug(t, `function f(): number { return 0; }`)
	mustContain(t, asm, ".cfi_startproc")
	mustContain(t, asm, ".cfi_def_cfa_offset 8")
	mustContain(t, asm, ".cfi_offset fp, -8")
	mustContain(t, asm, ".cfi_offset lr, -4")
	mustContain(t, asm, ".cfi_def_cfa_register fp")
	mustContain(t, asm, ".cfi_endproc")
}

// Release builds (no SourceFile) drop all .cfi_* directives, so
// the linker's `.eh_frame` for our code stays at zero bytes.
// Saves ~50 bytes per function.
func TestCFIDirectivesAbsentInReleaseMode(t *testing.T) {
	asm := compile(t, `function f(): number { return 0; }`)
	if strings.Contains(asm, ".cfi_") {
		t.Errorf("release build must not emit .cfi_* directives:\n%s", asm)
	}
}

// Leaf functions push extra callee-saved registers, so each gets its
// own .cfi_offset entry — but only in debug mode.
func TestCFIDirectivesCoverLeafSavedRegisters(t *testing.T) {
	asm := compileDebug(t, `function add(a: number, b: number): number { return a + b; }`)
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

// Arrays carry a 4-byte little-endian length prefix at base-4.
// The IR lowers `len(x)` to `<expr>; const 4; sub; load`. The
// const-imm + address-mode-sink peeps fold this all the way
// down to a single `ldr rD, [rB, #-4]` — one instruction
// instead of the unfused `ldr/push/ldr/push/pop/pop/sub/ldr`.
func TestLenOfArrayLoadsPrefix(t *testing.T) {
	asm := compile(t, `function f(): number {
		var a: number[] = [10, 20, 30];
		return len(a);
	}`)
	mustContain(t, asm, "ldr r0, [r1, #-4]")
	if strings.Contains(asm, "bl strlen") {
		t.Errorf("len(array) must not call strlen:\n%s", asm)
	}
}

// The bump allocator is always present (every binary needs the
// heap initialised by _start), but the cmp-bhi-grow shape only
// makes sense with the brk-based runtime. A pure-arithmetic
// program just inherits the runtime; no per-program toggle.
func TestAllocRuntimeAlwaysPresentWithBumpShape(t *testing.T) {
	asm := compile(t, `function f(): number { return 1 + 2; }`)
	mustContain(t, asm, "__lang_alloc")
	mustContain(t, asm, "ldr r1, =__lang_heap_ptr")
	if strings.Contains(asm, "bl malloc") {
		t.Errorf("alloc must not call libc malloc:\n%s", asm)
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
	asm := compile(t, `function f(): string { var s: string = "hi"; return s; }`)
	mustContain(t, asm, ".section .rodata")
	mustContain(t, asm, ".LStr_0:")
	mustContain(t, asm, `.asciz "hi"`)
	mustContain(t, asm, "ldr r0, =.LStr_0")
}

// `print(literal)` folds at compile time: the call collapses to
// a single inline `write(2)` against a `data + "\n"` buffer in
// .rodata, bypassing the `__lang_puts` helper entirely. Programs
// where every print is a literal don't even pull the helper into
// the binary.
func TestPrintLiteralFoldsToInlineWrite(t *testing.T) {
	asm := compile(t, `function main(): void { print("hi"); }`)
	// .rodata gets a print-only buffer with the trailing newline.
	mustContain(t, asm, ".LLineBuf_0:")
	mustContain(t, asm, `.ascii "hi\n"`)
	// Call site is inline: write(1, .LLineBuf_0, 3).
	mustContain(t, asm, "ldr r1, =.LLineBuf_0")
	mustContain(t, asm, "ldr r2, =3")
	// write(2) syscall = 4 on ARM EABI.
	mustContain(t, asm, "mov r7, #4")
	// And no `bl __lang_puts` — the helper isn't even emitted.
	if strings.Contains(asm, "bl __lang_puts") {
		t.Errorf("literal print should fold inline, not call __lang_puts:\n%s", asm)
	}
	if strings.Contains(asm, "__lang_puts:") {
		t.Errorf("__lang_puts helper should be elided when every print is literal:\n%s", asm)
	}
}

// Non-literal prints still go through `__lang_puts` (the writev
// helper). Folding only applies when the arg is a string literal
// known at compile time.
func TestPrintNonLiteralKeepsHelper(t *testing.T) {
	// Use a parameter to force a non-literal arg — `"hi" + "!"`
	// would otherwise fold to a single `OpConstStr "hi!"` and
	// trip the literal peephole instead.
	asm := compile(t, `function emit(s: string): void { print(s); }`)
	mustContain(t, asm, "bl __lang_puts")
}

// `%` lowers to `sdiv` + `mls` (multiply-subtract) inline — no
// libgcc __aeabi_idivmod call. Faster than the function-call
// shape and lets us drop the libgcc dependency under -nostdlib.
func TestModuloUsesSdivMls(t *testing.T) {
	asm := compile(t, `function f(): number { return 17 % 5; }`)
	mustContain(t, asm, "sdiv r2, r1, r0")
	mustContain(t, asm, "mls r0, r2, r0, r1")
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
// Use a parameter to defeat the `literal + literal` IR fold so
// the runtime path stays exercised.
func TestStringConcatLowersToRuntime(t *testing.T) {
	asm := compile(t, `function f(s: string): string { return s + "x"; }`)
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
	// `f` calls `g`, so it isn't a leaf — expect the original prologue.
	// In release builds (default) `.cfi_*` is suppressed, so the push
	// follows the label directly.
	if !strings.Contains(asm, "f:\n\tpush {fp, lr}") {
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
	// Use string returns (not print) so the literals land in
	// the regular string pool rather than the print-fold buffers.
	asm := compile(t, `
		function f(): string { return "a"; }
		function g(): string { return "a"; }
		function h(): string { return "b"; }
	`)
	mustContain(t, asm, ".LStr_0:")
	mustContain(t, asm, ".LStr_1:")
	if strings.Contains(asm, ".LStr_2:") {
		t.Errorf("third label should not exist (dedup), got:\n%s", asm)
	}
}

// Every emitted program is libc-free. The binary links with
// `-nostdlib` and contains only language code + direct svc 0
// syscalls. This test enumerates the libc symbols we used to
// rely on and asserts none of them survive in any program — a
// representative kitchen-sink program covers the syscall
// helpers (file I/O), the alloc path, the string runtime, and
// the print path.
func TestArm32NoLibcSymbols(t *testing.T) {
	asm := compile(t, `function main(): number {
		print("hi");
		var n: number = 17 % 5;
		var s: string = "a" + "b";
		if (s == "ab") { n = n + 1; }
		return n;
	}`)
	for _, sym := range []string{
		"bl puts", "bl printf",
		"bl malloc", "bl free", "bl calloc",
		"bl write\n", "bl read\n", "bl open\n", "bl close\n", "bl lseek",
		"bl strcmp", "bl strlen", "bl memcpy", "bl memset",
		"bl getenv", "bl exit\n",
		"bl __aeabi_idiv", "bl __aeabi_idivmod",
		"bl __errno_location", "__libc_start_main",
	} {
		if strings.Contains(asm, sym) {
			t.Errorf("nostdlib invariant: program must not reference %q\n%s", sym, asm)
		}
	}
}

// strcmp short-circuits on pointer equality and on length
// mismatch before doing any byte-level work; the bulk loop is
// word-grain (4 bytes per cmp). All three layers should appear
// in the assembly.
func TestArm32StrcmpShortCircuitsAndIsWordGrain(t *testing.T) {
	asm := compile(t, `function f(): boolean { return "abc" == "abc"; }`)
	// 1. Pointer equality short-circuit.
	mustContain(t, asm, "cmp r0, r1")
	mustContain(t, asm, "beq .Lscmp_eq")
	// 2. Length compare via the prefix.
	mustContain(t, asm, "ldr r2, [r0, #-4]")
	mustContain(t, asm, "ldr r3, [r1, #-4]")
	// 3. Word-grain bulk loop.
	mustContain(t, asm, ".Lscmp_word:")
	mustContain(t, asm, "ldr r12, [r0], #4")
}

// `read_file` now uses one `fstat64(2)` to discover file size
// instead of the previous `lseek SEEK_END ; lseek SEEK_SET`
// pair. Saves one syscall per call.
func TestArm32ReadFileUsesFstat(t *testing.T) {
	asm := compile(t, `function main(): number {
		match (read_file("x")) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
		return -1;
	}`)
	// fstat64 syscall = 197 on ARM EABI.
	mustContain(t, asm, "mov r7, #197")
	// We load st_size from offset 48 of the kernel's stat64 struct.
	mustContain(t, asm, "ldr r6, [sp, #48]")
	// And we no longer issue any lseek (syscall 19) from read_file.
	// Anchor with a newline so "#19" doesn't false-match "#197".
	if strings.Contains(asm, "mov r7, #19\n") {
		t.Errorf("read_file should no longer call lseek:\n%s", asm)
	}
}

// memcpy is word-grain on the bulk path (4 bytes per iter)
// with a byte-grain tail for the last < 4 bytes. The shape pins
// both halves so any future tweak that drops the bulk loop
// trips the test.
func TestArm32MemcpyWordGrainBulk(t *testing.T) {
	asm := compile(t, `function f(): string { return "a" + "b"; }`)
	mustContain(t, asm, ".Lmcp_word:")
	mustContain(t, asm, "ldr r12, [r1], #4")
	mustContain(t, asm, "str r12, [r0], #4")
	mustContain(t, asm, ".Lmcp_tail:")
}

// `print(non_literal)` falls back to `__lang_puts`, which uses
// a single `writev(2)` over a 2-iovec gather (string + newline).
// We exercise the helper via a non-literal arg so the print-fold
// peephole doesn't preempt it.
func TestArm32PutsUsesWritev(t *testing.T) {
	asm := compile(t, `function emit(s: string): void { print(s); }`)
	// writev syscall = 146 on ARM EABI.
	mustContain(t, asm, "mov r7, #146")
	// 2-iovec gather + iovcnt = 2.
	mustContain(t, asm, "mov r2, #2")
	// We construct the iovec on the stack: 2 × {base,len} = 16 bytes.
	mustContain(t, asm, "sub sp, sp, #16")
}

// `eprint(non_literal)` falls back to `__lang_eprint`, which
// uses a single `writev(2)` over a 2-iovec gather routed to
// fd 2. We exercise the helper via a non-literal arg so the
// eprint-fold peephole doesn't preempt it.
func TestArm32EprintUsesWritev(t *testing.T) {
	asm := compile(t, `function emit(s: string): void { eprint(s); }`)
	mustContain(t, asm, "mov r7, #146")
	mustContain(t, asm, "mov r0, #2") // fd 2
}

// `eprint(literal)` folds the same way `print(literal)` does,
// just routed to fd 2 instead of fd 1. Reuses the shared
// .LLineBuf_* pool so `print("x")` and `eprint("x")` in the
// same program compile to a single `data + "\n"` buffer.
func TestEprintLiteralFoldsToInlineWrite(t *testing.T) {
	asm := compile(t, `function main(): void { eprint("hi"); }`)
	mustContain(t, asm, ".LLineBuf_0:")
	mustContain(t, asm, `.ascii "hi\n"`)
	mustContain(t, asm, "mov r0, #2") // fd 2
	mustContain(t, asm, "ldr r1, =.LLineBuf_0")
	mustContain(t, asm, "ldr r2, =3")
	mustContain(t, asm, "mov r7, #4") // sys_write
	if strings.Contains(asm, "bl __lang_eprint") {
		t.Errorf("literal eprint should fold inline:\n%s", asm)
	}
	if strings.Contains(asm, "__lang_eprint:") {
		t.Errorf("__lang_eprint helper should be elided when every eprint is literal:\n%s", asm)
	}
}

// `write(literal)` folds against the existing string pool —
// `write` doesn't add a newline, so we reuse the `.LStr_*`
// data pointer that any other lang code already produces for
// the literal.
func TestWriteLiteralFoldsToInlineWrite(t *testing.T) {
	asm := compile(t, `function main(): void { write("hi"); }`)
	mustContain(t, asm, ".LStr_0:")
	mustContain(t, asm, `.asciz "hi"`)
	mustContain(t, asm, "mov r0, #1") // fd 1
	mustContain(t, asm, "ldr r1, =.LStr_0")
	mustContain(t, asm, "ldr r2, =2") // length, no newline
	mustContain(t, asm, "mov r7, #4")
	if strings.Contains(asm, "bl __lang_write") {
		t.Errorf("literal write should fold inline:\n%s", asm)
	}
	if strings.Contains(asm, "__lang_write:") {
		t.Errorf("__lang_write helper should be elided when every write is literal:\n%s", asm)
	}
}

// _start is the binary's entry point under -nostdlib. It must
// capture argc / argv / envp from the kernel-provided stack into
// .bss globals, align sp, init the heap, and exit_group on
// main's return.
func TestArm32StartCaptureAndExit(t *testing.T) {
	asm := compile(t, `function main(): number { return 0; }`)
	mustContain(t, asm, ".global _start")
	mustContain(t, asm, "ldr r0, [sp]")
	mustContain(t, asm, "ldr r3, =__lang_argc")
	mustContain(t, asm, "ldr r3, =__lang_envp")
	mustContain(t, asm, "bic sp, sp, #7")
	mustContain(t, asm, "bl __lang_heap_init")
	mustContain(t, asm, "bl main")
	// exit_group syscall = 248
	mustContain(t, asm, "mov r7, #248")
}

// The bump allocator's fast path is six instructions plus the
// fall-through: align size, load heap_ptr, add, compare against
// heap_end, bump (or branch to grow). Verifying all five line
// shapes in order pins the structure.
// `__lang_alloc`'s fast path is a pure in-process bump (no
// syscall): align up, load heap_ptr, add, bound-check against
// heap_end, branch to OOM-exit on overflow, store, return.
// The 64 MiB pre-reserved mmap region means we never call brk
// during alloc — Linux populates pages lazily on first touch.
func TestArm32AllocFastPath(t *testing.T) {
	asm := compile(t, `function f(): number[] { return [1, 2, 3]; }`)
	mustContain(t, asm, "add r0, r0, #3")
	mustContain(t, asm, "bic r0, r0, #3")
	mustContain(t, asm, "ldr r1, =__lang_heap_ptr")
	mustContain(t, asm, "ldr r12, =__lang_heap_end")
	mustContain(t, asm, "bhi .Lalloc_oom")
	// No grow-path label: the heap is pre-reserved; alloc has
	// no slow-path syscall.
	if strings.Contains(asm, ".Lalloc_grow") {
		t.Errorf("alloc should no longer have a grow path:\n%s", asm)
	}
	// And no `brk` syscall in the alloc helper either.
	if strings.Contains(asm, "mov r7, #45\n") {
		t.Errorf("alloc must not call brk:\n%s", asm)
	}
}

// `_start` reserves the bump arena via a single mmap2 syscall
// at heap_init time — modern allocator interface, no brk.
func TestArm32HeapInitUsesMmap(t *testing.T) {
	asm := compile(t, `function main(): number { return 0; }`)
	// mmap2 syscall = 192 on ARM EABI.
	mustContain(t, asm, "mov r7, #192")
	// We pass MAP_PRIVATE|MAP_ANONYMOUS = 0x22 in r3.
	mustContain(t, asm, "mov r3, #34")
	// And the kernel-chosen address goes into __lang_heap_ptr.
	mustContain(t, asm, "ldr r1, =__lang_heap_ptr")
}

// Floats now lower through VFPv2 — the assembly should declare
// the FPU and use the `vadd.f32` / `vmov` mnemonics rather than
// the old "not supported" error.
func TestArm32EmitsVFPForFloats(t *testing.T) {
	asm := compile(t, `function f(): float { return 1.5 + 2.25; }`)
	mustContain(t, asm, ".fpu vfpv2")
	mustContain(t, asm, "vadd.f32")
	mustContain(t, asm, "vmov")
}

// switch lowers to a chain of nested blocks: each case opens an
// outer "skip-on-no-match" + inner "match-target" block, with
// br_if on each value comparison. From the assembly side it
// shows up as a swarm of blkEnd labels and `bne`/`beq` jumps.
// The cmp+branch peephole collapses each comparison's 4-line
// boolean materialise into a single `b<cc>`, and the cmp-against-
// const peephole rewrites comparisons against small literals to
// `cmp rN, #imm` form. Each switch case ends up as a one-line
// cmp + conditional branch.
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
	// Each case lowers to `cmp rN, #<value>`.
	mustContain(t, asm, "cmp r1, #1")
	mustContain(t, asm, "cmp r1, #2")
	mustContain(t, asm, "cmp r1, #3")
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
	// `x += 7` lowers via the binary path; the const-imm
	// peephole folds the load + add trio into `add r0, r1, #7`.
	mustContain(t, asm, "add r0, r1, #7")
}

func TestArm32StringIndexLoadsByte(t *testing.T) {
	asm := compile(t, `function f(): number { var s: string = "abc"; return s[1]; }`)
	mustContain(t, asm, "ldrb")
}

// `"literal"[const_idx]` folds at compile time to the byte
// at that index. The literal data isn't even emitted in
// .rodata for this case — the byte is materialised inline.
func TestArm32StringIndexLiteralFolds(t *testing.T) {
	// 'b' is byte 0x62 = 98.
	asm := compile(t, `function f(): number { return "abc"[1]; }`)
	mustContain(t, asm, "ldr r0, =98")
	if strings.Contains(asm, `.asciz "abc"`) {
		t.Errorf("\"literal\"[const] must not emit the literal data:\n%s", asm)
	}
}

// `"a" + "b"` folds at compile time to a single literal "ab" —
// no `__lang_strcat` allocation, no memcpy. Programs without
// any non-literal concat shouldn't pull `__lang_strcat` into
// the binary at all.
func TestArm32ConcatLiteralFolds(t *testing.T) {
	asm := compile(t, `function f(): string { return "foo" + "bar"; }`)
	mustContain(t, asm, `.asciz "foobar"`)
	if strings.Contains(asm, "bl __lang_strcat") {
		t.Errorf("literal+literal concat must fold; __lang_strcat must not appear:\n%s", asm)
	}
	if strings.Contains(asm, "__lang_strcat:") {
		t.Errorf("__lang_strcat helper should be elided when no runtime concat exists:\n%s", asm)
	}
}

// `s == lit` (one-side non-literal) takes the length-short-
// circuit fast path: load `s.len`, compare against the
// literal's known length, and only call __lang_strcmp when
// they match. Saves the function call entirely on length
// mismatch — the common case for HTTP routing patterns like
// `path == "/health"`.
func TestArm32StringEqualityShortCircuitsOnLengthMismatch(t *testing.T) {
	asm := compile(t, `function f(s: string): boolean { return s == "ok"; }`)
	// `len(s)` materialises as the prefix-load shape...
	mustContain(t, asm, "ldr r0, [r0]")
	// ...compared against the literal length (2). The cmp-
	// against-const peephole folds the `ldr r0, =2 ; cmp rN, r0`
	// pair into a single `cmp rN, #2`.
	mustContain(t, asm, "cmp r1, #2")
	// And the byte-level compare still runs on length match.
	mustContain(t, asm, "bl __lang_strcmp")
}

// Both literals: fold to a const at compile time.
// `"a" == "a"` → 1, `"a" == "b"` → 0. No strcmp call at all.
func TestArm32StringEqualityLiteralFolds(t *testing.T) {
	asmEq := compile(t, `function f(): boolean { return "a" == "a"; }`)
	if strings.Contains(asmEq, "bl __lang_strcmp") {
		t.Errorf("lit == lit must fold; strcmp must not appear:\n%s", asmEq)
	}
	mustContain(t, asmEq, "ldr r0, =1")
	asmNeq := compile(t, `function f(): boolean { return "a" == "b"; }`)
	if strings.Contains(asmNeq, "bl __lang_strcmp") {
		t.Errorf("lit == lit must fold; strcmp must not appear:\n%s", asmNeq)
	}
	mustContain(t, asmNeq, "ldr r0, =0")
}

// `len(string)` no longer calls strlen — strings carry the same
// 4-byte length prefix as arrays, so the lowering threads through
// the same `<ptr>; const 4; sub; load` IR sequence.
// `len(literal)` folds at compile time to a const — no runtime
// pointer arithmetic, no prefix load. The literal isn't even
// emitted into .rodata for this case (nothing else references
// the data, just its length).
func TestArm32LenStringLiteralFolds(t *testing.T) {
	asm := compile(t, `function f(): number { return len("abc"); }`)
	mustContain(t, asm, "ldr r0, =3")
	if strings.Contains(asm, "ldr r0, =.LStr_") {
		t.Errorf("len(literal) must not load the string data:\n%s", asm)
	}
	if strings.Contains(asm, "sub r0, r1, r0") {
		t.Errorf("len(literal) must not compute prefix address:\n%s", asm)
	}
	if strings.Contains(asm, "bl strlen") {
		t.Errorf("len(string) must not call strlen:\n%s", asm)
	}
}

// Non-literal lens still go through the runtime prefix load.
// Both shapes (strings + arrays) bottom out in the same
// `<ptr>; const 4; sub; load` IR, so testing one suffices.
func TestArm32LenNonLiteralUsesPrefixLoad(t *testing.T) {
	asm := compile(t, `function f(s: string): number { return len(s); }`)
	// The const-imm + address-mode-sink peeps fold the entire
	// `<ptr>; const 4; sub; load` IR into a single
	// `ldr rD, [base, #-4]`.
	mustContain(t, asm, "ldr r0, [r1, #-4]")
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
// write the new prefix, and copy the bytes through. Use a
// parameter to defeat the literal-fold so the runtime path
// stays exercised.
func TestArm32StrcatReadsPrefixesAndAllocates(t *testing.T) {
	asm := compile(t, `function f(s: string): string { return s + "x"; }`)
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
