package wasm

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

func compileToWAT(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	wat, err := Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return wat
}

func mustContain(t *testing.T, wat, needle string) {
	t.Helper()
	if !strings.Contains(wat, needle) {
		t.Errorf("expected output to contain %q\n--- output ---\n%s", needle, wat)
	}
}

func mustNotContain(t *testing.T, wat, needle string) {
	t.Helper()
	if strings.Contains(wat, needle) {
		t.Errorf("expected output NOT to contain %q\n--- output ---\n%s", needle, wat)
	}
}

// Function values materialise as `{fn_idx, env}` pair-cell
// pointers; the `fn_idx` field stores the function's POSITION
// IN THE FUNCREF TABLE, not its position in `prog.Funcs`. The
// funcref table only includes functions actually referenced as
// values, so a value-referenced function declared later than
// some non-table functions still gets a low table index.
//
// This program declares two non-table functions before the
// value-referenced `target`, so funcIndex["target"] = 2 but
// tableIndex["target"] = 0. The cell `__closure_cell_target`
// must encode fn_idx = 0.
func TestFunctionValueUsesTableIndexNotFuncIndex(t *testing.T) {
	src := `function unrelated_a(x: i32): i32 { return x + 1; }
	function unrelated_b(x: i32): i32 { return x + 2; }
	function target(x: i32): i32 { return x * 10; }
	function apply(f: (i32) => i32, x: i32): i32 {
		return f(x);
	}
	function main(): i32 { return apply(target, 4); }`
	wat := compileToWAT(t, src)
	mustContain(t, wat, "(table $fns 1 funcref)")
	mustContain(t, wat, "(elem (i32.const 0) $target)")
}

func TestSimpleFunction(t *testing.T) {
	wat := compileToWAT(t, `function main(): i32 { return 42; }`)
	mustContain(t, wat, "(module")
	mustContain(t, wat, `(func $main (result i32)`)
	mustContain(t, wat, "i32.const 42")
	mustContain(t, wat, `(export "main" (func $main))`)
}

func TestArithmeticOps(t *testing.T) {
	// Use parameters to keep ops visible past constant folding
	// (literal arithmetic would collapse to a single i32.const).
	// `%` is intentionally still here — Fold deliberately leaves
	// rem_s alone so a `1 % 0` source bug surfaces at runtime
	// rather than getting silently masked.
	wat := compileToWAT(t, `function main(a: i32, b: i32): i32 { return 7 % 3 + (a & b); }`)
	mustContain(t, wat, "i32.rem_s")
	mustContain(t, wat, "i32.and")
	mustContain(t, wat, "i32.add")
}

func TestComparisonReturnsI32(t *testing.T) {
	wat := compileToWAT(t, `function f(a: i32, b: i32): boolean { return a < b; }`)
	mustContain(t, wat, "i32.lt_s")
	mustContain(t, wat, "(result i32)")
}

func TestShortCircuitAnd(t *testing.T) {
	wat := compileToWAT(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	// `&&` lowers to a conditional that yields i32 on both arms.
	mustContain(t, wat, "if (result i32)")
}

func TestIfElse(t *testing.T) {
	wat := compileToWAT(t, `function f(n: i32): i32 {
		if (n == 0) { return 1; } else { return 2; }
	}`)
	mustContain(t, wat, "if")
	mustContain(t, wat, "else")
	mustContain(t, wat, "end")
}

func TestWhileBreakContinue(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 {
		var i = 0;
		while (true) {
			if (i == 5) { break; }
			i = i + 1;
		}
		return i;
	}`)
	// while expands to outer `block` + `loop`, with `break` resolving
	// to a `br` that targets the outer block at relative depth 2 from
	// inside the if-body.
	mustContain(t, wat, "block")
	mustContain(t, wat, "loop")
	mustContain(t, wat, "br ")
}

func TestForLoopWithStep(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 {
		var sum = 0;
		for (var i = 0; i < 10; i = i + 1) {
			if (i < 5) { continue; }
			sum = sum + i;
		}
		return sum;
	}`)
	// `for` adds an inner `block` around the body so `continue` can
	// jump just past it (and so the step still runs before the next
	// iteration). br_if exits the outer block when the condition
	// fails.
	mustContain(t, wat, "block")
	mustContain(t, wat, "loop")
	mustContain(t, wat, "br_if")
}

func TestRecursionDirectCall(t *testing.T) {
	wat := compileToWAT(t, `function fact(n: i32): i32 {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}`)
	mustContain(t, wat, "call $fact")
}

func TestStringsLowerToLinearMemory(t *testing.T) {
	// Use a literal longer than the single-i32 inline cap so the
	// OpConstStr literal-pack short-circuit doesn't apply — heap
	// form is required to assert the data-segment + base offset
	// pinning below.
	wat := compileToWAT(t, `function main(): void { print("hello"); }`)
	// After step 6 every print goes through wasi:io/streams; the
	// preview-1 fd_write import is gone.
	mustContain(t, wat, `(import "wasi:io/streams@0.2.0" "[method]output-stream.blocking-write-and-flush"`)
	mustContain(t, wat, `(import "wasi:cli/stdout@0.2.0" "get-stdout"`)
	mustContain(t, wat, "(memory $mem 1)")
	mustContain(t, wat, `(func $print`)
	mustContain(t, wat, `(func $putchar`)
	mustContain(t, wat, "call $print")
	// Length-prefixed string entry: 5 bytes "hello" so prefix is \05\00\00\00.
	mustContain(t, wat, `\05\00\00\00hello`)
	// Preview-2 layout pushes the string base from 64 to 128 (the
	// canonical-ABI scratch slots reserve memory[92..127]); chars
	// for the first interned string therefore start at 128+4=132.
	mustContain(t, wat, "i32.const 132")
}

// Programs that don't touch strings, print or putchar still pull in
// the runtime preamble after step 6 — the component model exports
// `cabi_realloc` and the host always wires
// `wasi:io/streams.blocking-write-and-flush` whether the program
// uses it or not. The test now asserts the *minimum* shape under
// preview-2: stdio imports come in unconditionally, the bump
// allocator is required (cabi_realloc defers to it), and the
// preview-1 imports are gone.
func TestNoRuntimeWhenUnused(t *testing.T) {
	wat := compileToWAT(t, `function main(): i32 { return 42; }`)
	if strings.Contains(wat, "wasi_snapshot_preview1") {
		t.Errorf("preview-1 import leaked into WAT:\n%s", wat)
	}
	mustContain(t, wat, `(import "wasi:cli/stdout@0.2.0" "get-stdout"`)
	mustContain(t, wat, `$__lang_alloc`)
	mustContain(t, wat, `(export "cabi_realloc" (func $cabi_realloc))`)
}

// Array literals lower to bump-allocator + per-element store, with a
// synthetic i32 scratch local holding the base pointer between the
// alloc and the stores.
func TestArrayLitEmitsAllocAndStores(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 {
		var a: i32[] = [10, 20, 30];
		return a[1];
	}`)
	mustContain(t, wat, "(func $__lang_alloc")
	mustContain(t, wat, "(local $__scratch_0 i32)")
	// 4 (length prefix) + 3*4 (elements) = 16
	mustContain(t, wat, "i32.const 16")
	mustContain(t, wat, "call $__lang_alloc")
	// Indexing `a[1]` goes through the bounds-check helper.
	mustContain(t, wat, "call $__arr_idx")
	mustContain(t, wat, "i32.load")
}

// Index assignment lowers to i32.store and leaves nothing on the
// stack; no `drop` should be emitted by the surrounding ExprStmt.
func TestIndexAssignEmitsStore(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 {
		var a: i32[] = [0, 0];
		a[0] = 7;
		return a[0];
	}`)
	mustContain(t, wat, "i32.store")
}

// Function-typed parameters trigger a function table, type
// declarations for the indirect callee's signature, and a
// call_indirect at the call site. With the unified-closure ABI
// every callable in the table gets an extra `__env` param at
// the end — pure top-level fns ignore the env slot, hoisted
// closures read their captured block from it.
// Register-based Result/Option returns — step 2 transitional
// shape. The IR ops OpMakeSomeI32 + OpReturnPair currently
// `OpMakeSomeI32` + `OpReturnPair` in a pair-form function
// lowers as a real multi-value return on wasm: the function
// signs as `(result i32 i32)`, the maker reorders the stack
// to `[tag=0, payload]`, and `OpReturnPair` returns both
// values. No heap-box alloc on the function side.
func TestPairFormReturnEmitsMultiValueSig(t *testing.T) {
	prog := &ast.Program{
		Funcs: []*ast.FuncDecl{
			{
				Name:       "wrap",
				Params:     []ast.Param{{Name: "x", Type: ast.NumberType{}}},
				ReturnType: ast.EnumType{Name: "Option", Args: []ast.Type{ast.NumberType{}}},
				Body:       &ast.Block{},
			},
		},
	}
	ip := &ir.Program{
		Funcs: []*ir.Func{
			{
				Name:       "wrap",
				Params:     []ast.Param{{Name: "x", Type: ast.NumberType{}}},
				ReturnType: ast.EnumType{Name: "Option", Args: []ast.Type{ast.NumberType{}}},
				Ops: []ir.Op{
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpMakeSomeI32},
					{Kind: ir.OpReturnPair},
				},
			},
		},
		PairForm: map[string]bool{"wrap": true},
	}
	wat, err := EmitFromIR(prog, &checker.Info{}, ip)
	if err != nil {
		t.Fatalf("EmitFromIR: %v", err)
	}
	// Multi-value return signature.
	mustContain(t, wat, "(func $wrap (param $x i32) (result i32 i32)")
	// Pair-form constructor: no alloc, just reorder via
	// `$__pair_tmp` so the stack ends as [tag=0, payload].
	mustContain(t, wat, "local.set $__pair_tmp")
	mustContain(t, wat, "i32.const 0")
	mustContain(t, wat, "local.get $__pair_tmp")
	// And no heap-box alloc in this body — pair-form on
	// wasm is the real multi-value return now.
	mustNotContainInFunction(t, wat, "$wrap", "call $__lang_alloc")
}

// `OpMakeNoneI32` in a pair-form function emits a zero-alloc
// pair-form push (tag=1, payload=0) — the function signature
// is `(result i32 i32)` and the body just pushes the two
// constants before `OpReturnPair`.
func TestPairFormReturnNoneEmitsMultiValue(t *testing.T) {
	prog := &ast.Program{
		Funcs: []*ast.FuncDecl{
			{
				Name:       "lookup_miss",
				Params:     nil,
				ReturnType: ast.EnumType{Name: "Option", Args: []ast.Type{ast.NumberType{}}},
				Body:       &ast.Block{},
			},
		},
	}
	ip := &ir.Program{
		Funcs: []*ir.Func{
			{
				Name:       "lookup_miss",
				ReturnType: ast.EnumType{Name: "Option", Args: []ast.Type{ast.NumberType{}}},
				Ops: []ir.Op{
					{Kind: ir.OpMakeNoneI32},
					{Kind: ir.OpReturnPair},
				},
			},
		},
		PairForm: map[string]bool{"lookup_miss": true},
	}
	wat, err := EmitFromIR(prog, &checker.Info{}, ip)
	if err != nil {
		t.Fatalf("EmitFromIR: %v", err)
	}
	mustContain(t, wat, "(func $lookup_miss (result i32 i32)")
	mustNotContainInFunction(t, wat, "$lookup_miss", "call $__lang_alloc")
}

// mustNotContainInFunction extracts the function body of `fnName`
// from `wat` (matched on `(func $<fnName>` to the matching close
// paren depth) and asserts `needle` doesn't appear within it.
// Lets us inspect a single function's body in isolation without
// false positives from runtime helpers elsewhere in the module.
func mustNotContainInFunction(t *testing.T, wat, fnName, needle string) {
	t.Helper()
	body := extractFuncBody(wat, fnName)
	if body == "" {
		t.Errorf("function %s not found in WAT:\n%s", fnName, wat)
		return
	}
	if strings.Contains(body, needle) {
		t.Errorf("function %s contains %q (expected absent):\n%s", fnName, needle, body)
	}
}

func extractFuncBody(wat, fnName string) string {
	header := "(func " + fnName
	i := strings.Index(wat, header)
	if i < 0 {
		return ""
	}
	// Walk paren depth from i to find the matching close.
	depth := 0
	for j := i; j < len(wat); j++ {
		switch wat[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return wat[i : j+1]
			}
		}
	}
	return wat[i:]
}

func TestIndirectCallEmitsTable(t *testing.T) {
	wat := compileToWAT(t, `
		function add(a: i32, b: i32): i32 { return a + b; }
		function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
			return f(a, b);
		}
		function main(): i32 { return apply(add, 40, 2); }`)
	mustContain(t, wat, "(type $t0 (func (param i32) (param i32) (param i32) (result i32)))")
	mustContain(t, wat, "(table $fns 1 funcref)")
	mustContain(t, wat, "(elem (i32.const 0) $add)")
	mustContain(t, wat, "call_indirect (type $t0)")
}

// `var f = name` in source becomes a static closure-pair
// pointer; calling f derefs the pair (fn_idx + env_ptr) and
// dispatches via call_indirect.
func TestFunctionValueLocal(t *testing.T) {
	wat := compileToWAT(t, `
		function add(a: i32, b: i32): i32 { return a + b; }
		function main(): i32 {
			var f = add;
			return f(40, 2);
		}`)
	// Only `add` is in the table — main isn't referenced as a value.
	mustContain(t, wat, "(table $fns 1 funcref)")
	mustContain(t, wat, "call_indirect (type $t0)")
}

func TestFloatLiteralAndArithmetic(t *testing.T) {
	wat := compileToWAT(t, `function main(): f32 { return 1.5 + 2.5; }`)
	mustContain(t, wat, "(result f32)")
	mustContain(t, wat, "f32.const 1.5")
	mustContain(t, wat, "f32.const 2.5")
	mustContain(t, wat, "f32.add")
}

func TestFloatNegate(t *testing.T) {
	wat := compileToWAT(t, `function f(x: f32): f32 { return -x; }`)
	mustContain(t, wat, "f32.neg")
}

func TestFloatComparison(t *testing.T) {
	wat := compileToWAT(t, `function f(x: f32, y: f32): boolean { return x < y; }`)
	mustContain(t, wat, "f32.lt")
}

func TestFloatLocalAndParam(t *testing.T) {
	wat := compileToWAT(t, `function f(x: f32): f32 { var y: f32 = x; return y; }`)
	mustContain(t, wat, "(param $x f32)")
	mustContain(t, wat, "(local $y f32)")
}

func TestSwitchEmitsBlockAndScratch(t *testing.T) {
	wat := compileToWAT(t, `function f(n: i32): i32 {
		switch (n) {
			case 1: return 10;
			case 2, 3: return 20;
			default: return 99;
		}
		return -1;
	}`)
	// The tag is stashed in a synthetic i32 scratch slot. Each case
	// expands to a pair of nested blocks (skip-on-no-match outer +
	// match-target inner) using br_if for value comparisons.
	mustContain(t, wat, "(local $__scratch_0 i32)")
	mustContain(t, wat, "block")
	mustContain(t, wat, "i32.eq")
	mustContain(t, wat, "br_if")
}

func TestIfExprEmitsIfResult(t *testing.T) {
	wat := compileToWAT(t, `function f(b: boolean): i32 { return if (b) { 1 } else { 2 }; }`)
	mustContain(t, wat, "if (result i32)")
	mustContain(t, wat, "i32.const 1")
	mustContain(t, wat, "i32.const 2")
}

func TestIfExprFloatEmitsF32Result(t *testing.T) {
	wat := compileToWAT(t, `function f(b: boolean): f32 { return if (b) { 1.5 } else { 2.5 }; }`)
	mustContain(t, wat, "if (result f32)")
}

func TestCompoundAssignLowersToBinary(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 { var x: i32 = 5; x += 7; return x; }`)
	// `x += 7` lowers to `x = x + 7`. The IR encodes the tee with a
	// (store, load) pair the FuseTee pass collapses into a single
	// OpTeeLocal — codegen emits `local.tee $x`.
	mustContain(t, wat, "i32.const 7")
	mustContain(t, wat, "i32.add")
	mustContain(t, wat, "local.tee $x")
}

func TestStringIndexEmitsLoad8(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 { var s: string = "abc"; return s[1]; }`)
	mustContain(t, wat, "i32.load8_u")
}

// Use a parameter to defeat the IR-level lit==lit fold; we
// want to see the runtime $__str_eq path.
func TestStringEqualityEmitsHelper(t *testing.T) {
	wat := compileToWAT(t, `function f(s: string): boolean { return s == "ok"; }`)
	mustContain(t, wat, "$__str_eq")
	mustContain(t, wat, "call $__str_eq")
}

func TestLenOfStringRoutesThroughSSOHelper(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 { return len("hello"); }`)
	// `len(s)` lowers to OpStrLen, which the wasm backend
	// emits as a call into the SSO seam `$__lang_str_len`.
	// That helper branches on the top-bit inline flag: inline
	// form returns the 3-bit length from bits 24..26; heap
	// form falls back to the legacy `[ptr-4]` load. Both
	// branches are visible inside `$__lang_str_len`'s body.
	mustContain(t, wat, "call $__lang_str_len")
	mustContain(t, wat, "(func $__lang_str_len")
	if strings.Contains(wat, "call $len") {
		t.Errorf("expected len() to lower through OpStrLen, got call $len in:\n%s", wat)
	}
}

// `OpConstStr` literal-packs short literals (≤ 3 bytes) as
// inline-encoded i32 values instead of allocating a data-segment
// entry. The packed value matches `langstring.PackTinyWasm` byte
// layout: top-bit flag (0x80000000) + 3-bit length + up-to-3
// inline bytes. Saves `len + 4` data-segment bytes per unique
// short literal; the runtime cost is zero (still a single
// `i32.const`).
func TestOpConstStrShortLiteralPacksInline(t *testing.T) {
	// "ok" is 2 bytes: flag (0x80000000) | length 2 (<<24) |
	// 'o' (0x6f) | ('k' = 0x6b)<<8 = 0x82006b6f.
	wat := compileToWAT(t, `function f(): string { return "ok"; }`)
	mustContain(t, wat, "i32.const 0x82006b6f")
	mustNotContain(t, wat, `\02\00\00\00ok`)
}

// Longer literals (> 3 bytes) still emit through the heap-form
// `internString` path — one data-segment entry per unique
// literal plus an `i32.const <data-offset>` at each use site.
func TestOpConstStrLongLiteralStaysHeap(t *testing.T) {
	wat := compileToWAT(t, `function f(): string { return "longer"; }`)
	mustContain(t, wat, `\06\00\00\00longer`)
}

// $__str_concat outputs an inline-encoded i32 when the total
// length fits the single-i32 tiny SSO cap (≤3 bytes), keeping
// the alloc + length-prefix path for longer results. The
// emitted body shows both shapes — the early-exit return for
// the inline branch + the heap-path `$__lang_alloc` call.
func TestStrConcatHelperHasInlineOutputFastPath(t *testing.T) {
	wat := compileToWAT(t, `function f(s: string): string { return s + "b"; }`)
	mustContain(t, wat, "(func $__str_concat")
	mustContain(t, wat, "i32.const 0x80000000")
	mustContain(t, wat, "i32.le_u")
	mustContain(t, wat, "call $__lang_alloc")
	mustContain(t, wat, "call $__lang_str_byte")
}

// $__str_slice gains an inline-output fast path for `new_len ≤ 3`
// that mirrors the $__str_concat shape: build the inline-encoded
// i32 via byte reads + shift/OR rather than alloc + memory.copy.
// The body still keeps the heap-form path for longer slices,
// reachable via the `$__lang_alloc` call below the fast-path
// branch.
func TestStrSliceHelperHasInlineOutputFastPath(t *testing.T) {
	wat := compileToWAT(t, `function f(s: string): string { return s[0:2]; }`)
	mustContain(t, wat, "(func $__str_slice")
	mustContain(t, wat, "i32.const 0x80000000")
	mustContain(t, wat, "i32.le_u")
	mustContain(t, wat, "call $__lang_alloc")
}

// $string_from_bytes gains the same inline-output fast path for
// `bLen ≤ 3`: build the inline-encoded i32 by reading bytes
// straight out of the u8[] payload + shift/OR. Heap-form path
// is preserved for longer buffers via `$__lang_alloc`.
func TestStringFromBytesHelperHasInlineOutputFastPath(t *testing.T) {
	wat := compileToWAT(t, `function f(bs: u8[]): string { return string_from_bytes(bs); }`)
	mustContain(t, wat, "(func $string_from_bytes")
	mustContain(t, wat, "i32.const 0x80000000")
	mustContain(t, wat, "i32.le_u")
	mustContain(t, wat, "call $__lang_alloc")
}

// $args gains an inline-output fast path for argv entries whose
// NUL-terminated length is ≤ 3 (short CLI flags like "-h" / "-v",
// short command names like "go" / "rm"). Same shape as the rest
// of the SSO producer family — top-bit-flag constant + `i32.le_u`
// branch + per-byte shift/OR. The heap-form path is preserved
// for longer args via the same `$__lang_alloc` call below.
func TestArgsHelperHasInlineOutputFastPath(t *testing.T) {
	wat := compileToWAT(t, `function main(): i32 {
		var a: string[] = args();
		return len(a);
	}`)
	mustContain(t, wat, "(func $args")
	mustContain(t, wat, "i32.const 0x80000000")
	mustContain(t, wat, "i32.le_u")
	mustContain(t, wat, "call $__lang_alloc")
}

// `$__method_Reader_read_chunk` gains an inline-output fast
// path for short reads (n ≤ 3). Catches one-byte status probes
// + two-byte protocol tokens that otherwise allocate the same
// length-prefix + memcpy + Some(box) sequence as a full
// 64-byte chunk.
func TestReaderReadChunkHelperHasInlineOutputFastPath(t *testing.T) {
	src := `function main(): void {
		match (stdin().read_chunk(8)) {
			Some(s) => { print(s); },
			None => {}
		}
	}`
	wat := compileToWAT(t, src)
	mustContain(t, wat, "(func $__method_Reader_read_chunk")
	mustContain(t, wat, "i32.const 0x80000000")
	mustContain(t, wat, "i32.le_u")
	mustContain(t, wat, "call $__lang_alloc")
}

// `$tcp_recv` gains the same inline-output fast path for short
// network responses (≤ 3 bytes). Catches protocol tokens like
// "OK", "GO", "OK\n", short ACKs.
func TestTcpRecvHelperHasInlineOutputFastPath(t *testing.T) {
	src := `function main(): i32 {
		var listener: i32 = tcp_listen(0);
		var conn: i32 = tcp_accept(listener);
		print(tcp_recv(conn, 8));
		return 0;
	}`
	wat := compileToWAT(t, src)
	mustContain(t, wat, "(func $tcp_recv")
	mustContain(t, wat, "i32.const 0x80000000")
	mustContain(t, wat, "i32.le_u")
	mustContain(t, wat, "call $__lang_alloc")
}

// `$__stream_read_line` gains an inline-output fast path for
// lines whose accumulated length fits the single-i32 tiny cap
// (≤ 3 bytes, including the trailing newline). Common for
// interactive-prompt responses like "" / "y\n" / "ok\n". The
// heap-form path is preserved for longer lines via the same
// `$__lang_alloc` + `memory.copy` sequence the helper used
// before; the SSO branch is gated on `i32.le_u`.
func TestStreamReadLineHelperHasInlineOutputFastPath(t *testing.T) {
	src := `function main(): void {
		match (stdin().read_line()) {
			Some(s) => { print(s); },
			None => {}
		}
	}`
	wat := compileToWAT(t, src)
	mustContain(t, wat, "(func $__stream_read_line")
	mustContain(t, wat, "i32.const 0x80000000")
	mustContain(t, wat, "i32.le_u")
	// Heap path still alive for longer lines.
	mustContain(t, wat, "call $__lang_alloc")
}

// String runtime helpers that need a linear-memory address
// (I/O writes, $__str_idx, $__str_slice) promote inline-form
// inputs through `$__lang_str_to_heap` at function entry. The
// helper itself is unconditionally emitted alongside the SSO
// seam pair; pin its presence here so the entry-promotion
// pattern stays wired.
//
// Stream-write paths (`print`, `write`, `eprint`, `Writer.write`,
// `$tcp_send`, HTTP body write) no longer promote-to-heap —
// they route through `$__lang_str_data_ptr` (which spills inline
// values into a shared scratch slot without alloc). Address-
// arithmetic paths (`$__str_idx`, `$__str_slice`, `$env`,
// `$open_*`, etc.) still go through `$__lang_str_to_heap`
// because they need durable storage.
func TestSSOHelpersEmitted(t *testing.T) {
	wat := compileToWAT(t, `function f(s: string): i32 {
		print(s);
		return s[0];
	}`)
	mustContain(t, wat, "(func $__lang_str_len")
	mustContain(t, wat, "(func $__lang_str_byte")
	mustContain(t, wat, "(func $__lang_str_data_ptr")
	mustContain(t, wat, "(func $__lang_str_to_heap")
	// Stream-write seam: inline-aware data-pointer helper.
	mustContain(t, wat, "call $__lang_str_data_ptr")
	// Address-arithmetic seam: $__str_idx promotes inline inputs
	// to heap-form at entry via $__lang_str_to_heap.
	mustContain(t, wat, "call $__lang_str_to_heap")
}

// $print (and the other stream-write helpers) skip the
// $__lang_str_to_heap promote in favour of $__lang_str_data_ptr.
// This is the perf win the inline-aware print path delivers:
// programs that print inline-form strings (e.g. status codes
// from int_to_string, short header values, $__str_concat outputs)
// no longer pay the per-call heap-promote alloc.
func TestPrintHelperUsesInlineDataPtrSeam(t *testing.T) {
	wat := compileToWAT(t, `function main(): void { print("hi"); }`)
	// Pin the helper's call sequence — handle, $__lang_str_data_ptr,
	// $__lang_str_len, $__streams_write.
	mustContain(t, wat, "call $__lang_str_data_ptr")
	// The promote-to-heap helper might still be emitted because
	// it's unconditionally part of the SSO runtime; but `$print`
	// itself must not call it (would be a regression to the
	// per-print alloc).
	idx := strings.Index(wat, "(func $print")
	if idx < 0 {
		t.Fatalf("$print helper not emitted:\n%s", wat)
	}
	body := wat[idx:]
	end := strings.Index(body, "\n  (func ")
	if end < 0 {
		end = len(body)
	}
	if strings.Contains(body[:end], "call $__lang_str_to_heap") {
		t.Errorf("$print should not call $__lang_str_to_heap (regression to per-call alloc):\n%s", body[:end])
	}
}

func TestStructLitAllocatesAndStores(t *testing.T) {
	wat := compileToWAT(t, `struct P { x: i32, y: i32 }
		function main(): i32 {
			var p: P = P { x: 1, y: 2 };
			return p.x + p.y;
		}`)
	mustContain(t, wat, "(func $__lang_alloc")
	mustContain(t, wat, "(local $__scratch_0 i32)")
	mustContain(t, wat, "i32.const 8") // 2 fields × 4 bytes
	mustContain(t, wat, "i32.store")
	mustContain(t, wat, "i32.load")
}

func TestStringConcatEmitsHelper(t *testing.T) {
	// Use a parameter to defeat the IR-level `literal+literal`
	// fold (which would otherwise collapse `"a" + "b"` to a
	// single OpConstStr "ab" in the IR builder).
	wat := compileToWAT(t, `function f(s: string): string { return s + "b"; }`)
	mustContain(t, wat, "$__str_concat")
	mustContain(t, wat, "call $__str_concat")
}

func TestArrayIndexBoundsChecked(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 {
		var a: i32[] = [1, 2, 3];
		return a[0];
	}`)
	mustContain(t, wat, "$__arr_idx")
	mustContain(t, wat, "call $__arr_idx")
	mustContain(t, wat, "unreachable")
}

func TestStringIndexBoundsChecked(t *testing.T) {
	wat := compileToWAT(t, `function f(): i32 { var s: string = "abc"; return s[0]; }`)
	mustContain(t, wat, "$__str_idx")
	mustContain(t, wat, "call $__str_idx")
}

func TestClosureHoistsAndCaptures(t *testing.T) {
	wat := compileToWAT(t, `function makeAdder(n: i32): (i32) => i32 {
		function add(x: i32): i32 { return x + n; }
		return add;
	}
	function main(): i32 {
		var f = makeAdder(7);
		return f(35);
	}`)
	// The inner function gets hoisted with a synthetic name and an
	// __env parameter; closure cells live at offset 64.
	mustContain(t, wat, "$__closure_add_")
	mustContain(t, wat, "(param $__env i32)")
	// The env block stores the captured var.
	mustContain(t, wat, "$__env_scratch")
	// Defunctionalisation + factory-flow analysis recognise that
	// makeAdder always returns the same closure target, so the
	// call site dispatches directly. No call_indirect, no
	// closure-pair allocation in main.
	mustNotContain(t, wat, "call_indirect")
}
