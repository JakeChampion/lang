package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// lowerSource parses, type-checks, and lowers src to IR. The check is
// expected to pass; failures stop the test.
func lowerSource(t *testing.T, src string) *Program {
	return lowerSourceWith(t, src, 4)
}

// lowerSourceWith is the pointer-width-aware sibling of
// `lowerSource`. Tests that care about target-aware ABI
// decisions (e.g. pair-form pointer-payload eligibility, which
// is wasm-only today) pass `ptrW = 8` to exercise the native
// path explicitly. The default `lowerSource` keeps ptrW=4 so
// existing tests don't have to thread the parameter.
func lowerSourceWith(t *testing.T, src string, ptrW int) *Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	ir, err := LowerWith(prog, info, ptrW)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ir
}

func mustContainOp(t *testing.T, p *Program, fnName string, want OpKind) {
	t.Helper()
	if hasOp(p, fnName, want) {
		return
	}
	t.Errorf("expected %s in %s; ops:\n%s", want, fnName, p)
}

func hasOp(p *Program, fnName string, want OpKind) bool {
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == want {
				return true
			}
		}
	}
	return false
}

func TestLowerSimpleArithmetic(t *testing.T) {
	p := lowerSource(t, `function f(): i32 { return 1 + 2 * 3; }`)
	if len(p.Funcs) != 1 {
		t.Fatalf("got %d funcs", len(p.Funcs))
	}
	got := p.Funcs[0].Ops
	want := []OpKind{
		OpConstI32, // 1
		OpConstI32, // 2
		OpConstI32, // 3
		OpMul,
		OpAdd,
		OpReturn,
	}
	if len(got) != len(want) {
		t.Fatalf("op count mismatch: got %d, want %d:\n%s", len(got), len(want), p)
	}
	for i, w := range want {
		if got[i].Kind != w {
			t.Errorf("op[%d] = %s, want %s", i, got[i].Kind, w)
		}
	}
}

func TestLowerLocals(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var x: i32 = 5;
		var y: i32 = x + 1;
		return y;
	}`)
	mustContainOp(t, p, "f", OpStoreLocal)
	mustContainOp(t, p, "f", OpLoadLocal)
}

func TestLowerIfElse(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): i32 {
		if (n == 0) { return 1; } else { return 2; }
	}`)
	mustContainOp(t, p, "f", OpIf)
	mustContainOp(t, p, "f", OpElse)
	mustContainOp(t, p, "f", OpEnd)
	mustContainOp(t, p, "f", OpEq)
}

func TestLowerWhileBreakContinue(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var i: i32 = 0;
		while (i < 10) {
			if (i == 5) { break; }
			i = i + 1;
		}
		return i;
	}`)
	// while expands to: outer block + loop, with br_if exiting the
	// block on the inverted condition; `break` becomes an OpBr that
	// targets the outer block at relative depth 1.
	mustContainOp(t, p, "f", OpBlock)
	mustContainOp(t, p, "f", OpLoop)
	mustContainOp(t, p, "f", OpBrIf)
	mustContainOp(t, p, "f", OpBr)
}

func TestLowerForLoopWithStep(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var sum: i32 = 0;
		for (var i: i32 = 0; i < 10; i = i + 1) {
			sum = sum + i;
		}
		return sum;
	}`)
	// for expands to: outer block (break) + loop + inner block
	// (continue) wrapping the body, then step + back-edge.
	mustContainOp(t, p, "f", OpBlock)
	mustContainOp(t, p, "f", OpLoop)
	mustContainOp(t, p, "f", OpBrIf) // condition exit
	mustContainOp(t, p, "f", OpBr)   // back-edge
	mustContainOp(t, p, "f", OpEnd)
}

func TestLowerDirectCall(t *testing.T) {
	p := lowerSource(t, `function add(a: i32, b: i32): i32 { return a + b; }
		function main(): i32 { return add(2, 3); }`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	hasDirect := false
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "add" && op.I32 == 2 {
			hasDirect = true
		}
	}
	if !hasDirect {
		t.Errorf("expected call $add (argc=2), ops:\n%s", p)
	}
}

func TestLowerIndirectCall(t *testing.T) {
	p := lowerSource(t, `function add(a: i32, b: i32): i32 { return a + b; }
		function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
			return f(a, b);
		}`)
	apply := findFunc(p, "apply")
	if apply == nil {
		t.Fatal("apply not found")
	}
	hasIndirect := false
	for _, op := range apply.Ops {
		if op.Kind == OpCallIndirect {
			hasIndirect = true
		}
	}
	if !hasIndirect {
		t.Errorf("expected call_indirect, ops:\n%s", p)
	}
}

func TestLowerShortCircuitAnd(t *testing.T) {
	p := lowerSource(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	// `a && b` lowers to a typed if/else: when a is truthy the body
	// pushes b, otherwise it pushes a normalised 0. Both arms thread
	// an i32 result.
	if !strings.Contains(p.String(), "if i32") {
		t.Errorf("expected `if i32` in lowered output:\n%s", p)
	}
	mustContainOp(t, p, "f", OpElse)
}

func TestLowerFloatArithmetic(t *testing.T) {
	p := lowerSource(t, `function f(): f32 { return 1.5 + 2.5; }`)
	mustContainOp(t, p, "f", OpFAdd)
	mustContainOp(t, p, "f", OpConstF32)
}

func TestLowerImplicitReturn(t *testing.T) {
	p := lowerSource(t, `function f(): void { var x: i32 = 0; }`)
	last := p.Funcs[0].Ops[len(p.Funcs[0].Ops)-1]
	if last.Kind != OpReturnVoid {
		t.Errorf("expected trailing return_void, got %s", last.Kind)
	}
}

// A value-returning function whose body doesn't end in an explicit
// return still gets a synthetic trailing pad-const + return at the IR
// level (defensive — the path past the loop is unreachable). The source
// uses an infinite `while (true)` loop because a function that simply
// falls off the end is now a checker error (E052); the infinite loop is
// the valid shape that still has no explicit trailing return. See
// docs/ADVERSARIAL-REVIEW-2026-06.md (F4).
func TestLowerImplicitReturnNumber(t *testing.T) {
	p := lowerSource(t, `function f(): i32 { while (true) { var x: i32 = 0; } }`)
	ops := p.Funcs[0].Ops
	if ops[len(ops)-1].Kind != OpReturn {
		t.Errorf("expected trailing return, got %s", ops[len(ops)-1].Kind)
	}
	if ops[len(ops)-2].Kind != OpConstI32 {
		t.Errorf("expected pad const before return, got %s", ops[len(ops)-2].Kind)
	}
}

func findFunc(p *Program, name string) *Func {
	for _, fn := range p.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}

// isNamedCallKind reports whether the op kind carries a callee name in
// Str — OpCallDirect plus the dedicated rc ops (#4402 opt 2), so rc-op
// assertions keep working across the kinds split.
func isNamedCallKind(k OpKind) bool {
	return k == OpCallDirect || k == OpRcInc || k == OpRcDec || k == OpRcIsUnique
}

func countCallDirect(ops []Op, name string) int {
	n := 0
	for _, op := range ops {
		if isNamedCallKind(op.Kind) && op.Str == name {
			n++
		}
	}
	return n
}

// Phase 1e-closures-ii: a FuncType (closure) value is rc-tracked
// like any other heap value — aliasing one inc's its rc=1 header,
// and closure locals are dec'd at function exit. The closure
// captures `n` so it lowers to a heap pair/env (not a static
// cell), giving a real rc to bump. Checked at the IR level (post
// LowerWith, before the backend's defunctionalise / elide passes)
// so the assertion is deterministic.
func TestLowerClosureValueRcTracked(t *testing.T) {
	// `b = a` while `a` is still live afterwards (used at `a()`) is a
	// genuine alias, not a move — so it must inc the closure value;
	// the closure locals are dec'd at exit. (A single-use chain would
	// instead take the move-on-alias path and elide the inc, which is
	// also correct — this shape pins the inc-on-live-alias case.)
	src := `function main(): i32 {
    var n: i32 = 5;
    function f(): i32 { return n; }
    var a = f;
    var b = a;
    return a() + b();
}`
	prog := lowerSourceWith(t, src, 8)
	fn := findFunc(prog, "main")
	if fn == nil {
		t.Fatal("main not found")
	}
	if got := countCallDirect(fn.Ops, "__fern_rc_inc"); got < 1 {
		t.Errorf("live closure alias `b = a` must emit __fern_rc_inc, got %d:\n%s", got, prog)
	}
	// Owned closure locals are reclaimed at exit via __fern_closure_drop
	// (this closure captures only a scalar, so the generic env-free
	// helper, not a per-closure thunk). Stages 2-3 replaced the old
	// flat __fern_rc_dec drop for FuncType locals.
	if got := countCallDirect(fn.Ops, "__fern_closure_drop"); got < 1 {
		t.Errorf("closure locals must be reclaimed at exit, got %d __fern_closure_drop:\n%s", got, prog)
	}
}

func TestLowerIfExpr(t *testing.T) {
	prog := lowerSource(t, `function f(b: boolean): i32 { return if (b) { 1 } else { 2 }; }`)
	// IfExpr lowers to a typed `if i32 ... else ... end`.
	mustContainOp(t, prog, "f", OpIf)
	mustContainOp(t, prog, "f", OpElse)
	mustContainOp(t, prog, "f", OpEnd)
	mustContainOp(t, prog, "f", OpConstI32)
}

func TestLowerArrayLitAndIndex(t *testing.T) {
	prog := lowerSource(t, `function f(): i32 {
		var a: i32[] = [10, 20, 30];
		return a[1];
	}`)
	mustContainOp(t, prog, "f", OpAlloc)
	mustContainOp(t, prog, "f", OpStore) // length prefix + element stores
	// Indexing dispatches via __arr_idx then OpLoad.
	mustContainOp(t, prog, "f", OpCallDirect)
	mustContainOp(t, prog, "f", OpLoad)
}

func TestLowerStringIndex(t *testing.T) {
	prog := lowerSource(t, `function f(): i32 {
		var s: string = "abc";
		return s[1] as i32;
	}`)
	mustContainOp(t, prog, "f", OpCallDirect) // __str_idx
	mustContainOp(t, prog, "f", OpLoadByte)
}

func TestLowerStructLitAndFieldAccess(t *testing.T) {
	prog := lowerSource(t, `struct P { x: i32, y: i32 }
		function main(): i32 {
			var p: P = P { x: 10, y: 32 };
			return p.x + p.y;
		}`)
	mustContainOp(t, prog, "main", OpAlloc)
	mustContainOp(t, prog, "main", OpStore)
	mustContainOp(t, prog, "main", OpLoad)
}

// Field access through an array index — `xs[i].field` where xs
// is T[]. The IR's fieldOwner historically only knew about
// Ident / FieldAccess / StructLit / CaptureRef; an Index target
// dropped through to "" and surfaced "ir: field access on
// unresolved struct" at lower time. Now handled via exprStaticType
// peeling the ArrayType.Elem.
func TestLowerFieldAccessThroughArrayIndex(t *testing.T) {
	prog := lowerSource(t, `struct P { x: i32 }
		function main(): i32 {
			var ps: P[] = [P { x: 7 }];
			return ps[0].x;
		}`)
	mustContainOp(t, prog, "main", OpLoad)
}

// Field access on a Call return — `foo().field` where foo
// returns a struct. The IR's fieldOwner historically only knew
// about Ident / FieldAccess / Index / StructLit / CaptureRef;
// a Call target dropped through to "" and surfaced "ir: field
// access on unresolved struct" at lower time. Now handled via
// the Call case + callReturnType helper which looks up the
// callee's return type in info.FuncSigs.
func TestLowerFieldAccessOnCallReturn(t *testing.T) {
	prog := lowerSource(t, `struct P { x: i32 }
		function make(): P { return P { x: 99 }; }
		function main(): i32 {
			return make().x;
		}`)
	mustContainOp(t, prog, "main", OpLoad)
}

// `literal + literal` folds at compile time to a single
// OpConstStr; the runtime OpStrConcat (and the `__fern_strcat`
// it bottoms out in) only fires on at least one non-literal arg.
func TestLowerStringConcatFoldsLiterals(t *testing.T) {
	prog := lowerSource(t, `function f(): string { return "a" + "b"; }`)
	if hasOp(prog, "f", OpStrConcat) {
		t.Errorf("literal + literal must fold; OpStrConcat must not appear:\n%s", prog)
	}
	mustContainOp(t, prog, "f", OpConstStr)
}

func TestLowerStringConcatNonLiteralKeepsRuntime(t *testing.T) {
	prog := lowerSource(t, `function f(s: string): string { return s + "x"; }`)
	mustContainOp(t, prog, "f", OpStrConcat)
}

// `len(s)` for a non-literal string argument lowers to OpStrLen.
// String length lives in a single IR seam so future small-string-
// optimisation work can change the encoding without hunting down
// every open-coded `[ptr - 4]` load across backend codegen.
// Payloadless variants lower to OpEnumSentinel (a static-sentinel
// push parameterised by tag value) rather than the standard
// alloc + tag-store sequence. Saves one __fern_alloc per
// construction; the produced address still has `[ptr + 0] = tag`
// so existing match / try codegen reads it correctly without
// consumer changes. Covers Option.None (tag 1), IoError
// payloadless variants, JsonValue.JNull, and any user-defined
// payloadless variant.
// `function f(): Option[i32] { return None; }` is detected as
// pair-form-eligible by findPairFormFuncs and lowers to
// OpMakeNoneI32 + OpReturnPair (zero-alloc on wasm; heap-box
// fallback on natives). The old sentinel path remains for
// non-eligible callers (payloadless variants in non-Option
// enums, or Option[T] where T isn't an i32-stack shape).
func TestLowerNoneEmitsPairForm(t *testing.T) {
	prog := lowerSource(t, `function f(): Option[i32] { return None; }`)
	mustContainOp(t, prog, "f", OpMakeNoneI32)
	mustContainOp(t, prog, "f", OpReturnPair)
	fn := findFunc(prog, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpAlloc {
			t.Fatalf("None lowering still emits OpAlloc; pair-form rewrite slipped:\n%s", prog)
		}
		if op.Kind == OpEnumSentinel {
			t.Fatalf("pair-form None should bypass OpEnumSentinel:\n%s", prog)
		}
	}
}

// `Some(x)` in a pair-form-eligible function emits
// OpMakeSomeI32 + OpReturnPair, NOT OpAlloc.
func TestLowerSomeEmitsPairForm(t *testing.T) {
	prog := lowerSource(t, `function f(): Option[i32] { return Some(42); }`)
	mustContainOp(t, prog, "f", OpMakeSomeI32)
	mustContainOp(t, prog, "f", OpReturnPair)
	fn := findFunc(prog, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpAlloc {
			t.Fatalf("Some in pair-form function should not OpAlloc:\n%s", prog)
		}
	}
}

// `return if (c) { Ok(x) } else { Ok(y) };` keeps the function
// pair-form-eligible: both arms are variant literals, so the
// eligibility check accepts the IfExpr and the emitter lowers
// each arm via OpMakeOkI32 inside an `(if (result i32 i32))`
// block. The trailing OpReturnPair consumes the pair the block
// leaves on the stack. Without this shape the IR fell back to
// the generic heap-box path, which produced a single-i32 heap
// pointer that mismatched the function's `(result i32 i32)`
// wasm signature — wasmtime rejected the module at validation
// time (the seed-1423 / seed-1480 emit gaps).
func TestLowerReturnIfWithPairFormArmsStaysPair(t *testing.T) {
	prog := lowerSource(t, `function f(c: boolean): Result[i32, i32] {
		return if (c) { Ok(1) } else { Ok(2) };
	}`)
	mustContainOp(t, prog, "f", OpMakeOkI32)
	mustContainOp(t, prog, "f", OpReturnPair)
	fn := findFunc(prog, "f")
	// The if-block must use the multi-value `(i32, i32)` blocktype
	// (reused from BlockTypeStringPair); a plain `(if (result
	// i32))` would mean one arm's pair fell back to a single
	// pointer, which is exactly the wasm-side type mismatch the
	// fix prevents.
	sawPairIf := false
	for _, op := range fn.Ops {
		if op.Kind == OpIf && op.I32 == BlockTypeStringPair {
			sawPairIf = true
		}
		if op.Kind == OpAlloc {
			t.Fatalf("pair-form return with IfExpr should not OpAlloc:\n%s", prog)
		}
	}
	if !sawPairIf {
		t.Fatalf("expected `if (result i32 i32)` block in IfExpr-wrapped pair-form return:\n%s", prog)
	}
}

// Option[<pointer-shaped>] is NOT pair-form-eligible — the
// native fallback's i32 payload-store would truncate an
// 8-byte heap pointer on arm64-darwin. Falls through to the
// old heap-box path (OpEnumSentinel for None; OpAlloc for
// Some). Uses `Option[string]` to stay clear of any
// prelude-side struct names.
// Calls to pair-form functions go through OpCallDirectPair
// at the IR layer. The op carries the same Str/I32 args as
// OpCallDirect; backends interpret it target-appropriately
// (wasm extracts (tag, payload) from the heap-box pointer
// the function returns; natives do the equivalent
// `mov rax / ldr w0` + tag/payload load shape).
func TestLowerCallToPairFormFnEmitsOpCallDirectPair(t *testing.T) {
	prog := lowerSource(t, `function check(): Option[i32] { return Some(7); }
function main(): i32 {
    match (check()) {
        Some(v) => { return v; },
        None    => { return 99; }
    }
}`)
	mustContainOp(t, prog, "main", OpCallDirectPair)
}

// `if let Variant(...) = heap-form-scrutinee { ... }` lowers
// the tag read through OpMatchTag (the step-3 abstraction
// over "read the variant tag" from a heap-pointer scrutinee).
// Pair-form scrutinees take the zero-alloc fast path
// (TestLowerIfLetOnPairFormCallSkipsRebox below) which does
// NOT go through OpMatchTag.
func TestLowerIfLetUsesOpMatchTag(t *testing.T) {
	prog := lowerSource(t, `enum Color { Red(i32), Green, Blue }
function main(): i32 {
    var c: Color = Red(7);
    if let Red(v) = c { return v; }
    return 0;
}`)
	mustContainOp(t, prog, "main", OpMatchTag)
}

// `if let Some(v) = pair_form_call()` triggers the zero-alloc
// fast path. The IR emits OpCallDirectPair WITHOUT the
// emitRepackPairAsHeapBox rebox — the (tag, payload) pair
// flows directly from the call into two scratch locals, and
// the tag dispatch reads from the tag local (no OpMatchTag
// heap-load).
func TestLowerIfLetOnPairFormCallSkipsRebox(t *testing.T) {
	prog := lowerSource(t, `function pick(): Option[i32] { return Some(7); }
function main(): i32 {
    if let Some(v) = pick() { return v; }
    return 0;
}`)
	fn := findFunc(prog, "main")
	if fn == nil {
		t.Fatal("main not found")
	}
	// Pair-form call present.
	hasPairCall := false
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirectPair {
			hasPairCall = true
			break
		}
	}
	if !hasPairCall {
		t.Fatalf("expected OpCallDirectPair in main:\n%s", prog)
	}
	// The rebox path uses OpAlloc; the pair-form fast path
	// does not.
	for _, op := range fn.Ops {
		if op.Kind == OpAlloc {
			t.Fatalf("pair-form if-let should skip OpAlloc rebox:\n%s", prog)
		}
		// The pair-form path consumes (tag, payload) directly
		// into locals; it bypasses OpMatchTag (which is the
		// heap-pointer tag-load abstraction).
		if op.Kind == OpMatchTag {
			t.Fatalf("pair-form if-let should skip OpMatchTag heap-load:\n%s", prog)
		}
	}
}

// `emitRepackPairAsHeapBox` for pointer-shape payloads (string
// / array / struct / enum / slice / tuple / closure) uses the
// target's pointer width: 8-byte / +4 layout on wasm32, 16-byte
// / +8 layout on natives. Without the ptrW gate the wasm path
// would store payload at +8 (the native shape) but every reader
// — TryOp's success-path load at +4, `match`'s heap-payload
// load — pulls from +4, returning 0 and trapping downstream.
//
// Pin both layouts: ptrW=4 → rebox alloc is `OpConstI32 8`;
// ptrW=8 → `OpConstI32 16`.
func TestLowerRepackPairAsHeapBoxWasmLayout(t *testing.T) {
	src := `function pick(): Option[string] { return Some("yo"); }
function main(): i32 {
    var s: string = match (pick()) {
        Some(v) => v,
        None => ""
    };
    return s.len();
}`
	// ptrW=4 (wasm). The two-word string ABI excludes string
	// payloads from pair-form eligibility (only one i32 slot
	// per payload, but a string needs two). `pick` should NOT
	// be in PairForm and `main` should not contain a rebox
	// pattern.
	prog := lowerSourceWith(t, src, 4)
	if prog.PairForm["pick"] {
		t.Errorf("pick must not be pair-form on wasm32 (string payload needs two-word ABI):\n%s", prog)
	}
}

func TestLowerRepackPairAsHeapBoxNativeLayout(t *testing.T) {
	src := `function pick(): Option[string] { return Some("yo"); }
function main(): i32 {
    var s: string = match (pick()) {
        Some(v) => v,
        None => ""
    };
    return s.len();
}`
	// ptrW=8 (native). Pair-form Option[string] generic-position
	// call reboxes into a heap box: 16-byte payload area (payload
	// at +8, the natural alignment for the 8-byte pointer payload)
	// plus the Phase 1e-enums-ii 8-byte rc header → 24-byte alloc.
	prog := lowerSourceWith(t, src, 8)
	fn := findFunc(prog, "main")
	if fn == nil {
		t.Fatal("main not found")
	}
	if !reboxAllocSizePresent(fn.Ops, 24) {
		t.Errorf("ptrW=8 rebox must alloc 24 bytes (16-byte Option[string] box + 8-byte rc header):\n%s", prog)
	}
}

func reboxAllocSizePresent(ops []Op, size int32) bool {
	// Find an `OpConstI32 size` immediately followed by `OpAlloc` —
	// the rebox's `boxSize + rcHeaderBytes` const + alloc shape.
	// There are other alloc sites, so this is a presence check,
	// not a uniqueness check.
	for i := 0; i+1 < len(ops); i++ {
		if ops[i].Kind == OpConstI32 && ops[i].I32 == size && ops[i+1].Kind == OpAlloc {
			return true
		}
	}
	return false
}

// `match` on a heap-form scrutinee shares the OpMatchTag
// path with if-let / let-else. Pair-form match scrutinees
// take the zero-alloc fast path (see TestLowerMatchOn-
// PairFormCallSkipsRebox below) and do NOT go through
// OpMatchTag.
func TestLowerMatchUsesOpMatchTag(t *testing.T) {
	prog := lowerSource(t, `enum Color { Red(i32), Green, Blue }
function main(): i32 {
    var c: Color = Green;
    match (c) {
        Red(v) => { return v; },
        Green  => { return 1; },
        Blue   => { return 2; }
    }
}`)
	mustContainOp(t, prog, "main", OpMatchTag)
}

// `match (pair_form_call()) { ... }` triggers the zero-alloc
// fast path: scrutinee evaluates to (tag, payload) on the
// operand stack; both go to scratch locals; per-arm tag
// comparison reads from the tag local; binding extraction
// reads from the payload local.
func TestLowerMatchOnPairFormCallSkipsRebox(t *testing.T) {
	prog := lowerSource(t, `function pick(): Option[i32] { return Some(7); }
function main(): i32 {
    match (pick()) {
        Some(x) => { return x; },
        None    => { return 99; }
    }
}`)
	fn := findFunc(prog, "main")
	if fn == nil {
		t.Fatal("main not found")
	}
	hasPairCall := false
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirectPair {
			hasPairCall = true
			break
		}
	}
	if !hasPairCall {
		t.Fatalf("expected OpCallDirectPair in main:\n%s", prog)
	}
	for _, op := range fn.Ops {
		if op.Kind == OpAlloc {
			t.Fatalf("pair-form match should skip OpAlloc rebox:\n%s", prog)
		}
		if op.Kind == OpMatchTag {
			t.Fatalf("pair-form match should skip OpMatchTag heap-load:\n%s", prog)
		}
	}
}

// User-defined enums matching the canonical "1 payload-
// carrying variant at index 0 + 1 nullary variant at index 1"
// shape opt into pair-form like Option / Result. The IR reuses
// OpMakeSomeI32 / OpMakeNoneI32 as the generic tag-0 / tag-1
// constructor ops because the backends treat the four
// builtin maker ops as one tag-keyed family.
func TestLowerUserEnumPairFormEligible(t *testing.T) {
	prog := lowerSource(t, `enum Maybe { Just(i32), Nothing }
function pick(x: i32): Maybe {
    if (x < 0) { return Nothing; }
    return Just(x);
}
function main(): i32 {
    match (pick(5)) {
        Just(v)  => { return v; },
        Nothing  => { return -1; }
    }
}`)
	fn := findFunc(prog, "pick")
	if fn == nil {
		t.Fatal("pick not found")
	}
	hasSome := false // OpMakeSomeI32 used as the generic tag-0 ctor
	hasNone := false // OpMakeNoneI32 used as the generic tag-1 ctor
	hasReturnPair := false
	for _, op := range fn.Ops {
		switch op.Kind {
		case OpMakeSomeI32:
			hasSome = true
		case OpMakeNoneI32:
			hasNone = true
		case OpReturnPair:
			hasReturnPair = true
		case OpAlloc:
			t.Fatalf("user enum pair-form should not OpAlloc:\n%s", prog)
		}
	}
	if !hasSome || !hasNone || !hasReturnPair {
		t.Fatalf("expected OpMakeSomeI32 + OpMakeNoneI32 + OpReturnPair in pick:\n%s", prog)
	}
	// Caller match consumes the pair via OpCallDirectPair.
	mainFn := findFunc(prog, "main")
	if mainFn == nil {
		t.Fatal("main not found")
	}
	hasPairCall := false
	for _, op := range mainFn.Ops {
		if op.Kind == OpCallDirectPair && op.Str == "pick" {
			hasPairCall = true
			break
		}
	}
	if !hasPairCall {
		t.Fatalf("expected OpCallDirectPair to pick in main:\n%s", prog)
	}
}

// A user enum whose payload-carrying variant is at index 1
// (wrong order) is NOT pair-form-eligible — the IR's tag
// convention (0 = payload, 1 = nullary) wouldn't agree with
// the user decl's varIdx assignment, which would silently
// miscompile match dispatches.
func TestLowerUserEnumWrongOrderStaysHeapForm(t *testing.T) {
	prog := lowerSource(t, `enum Maybe { Nothing, Just(i32) }
function pick(x: i32): Maybe {
    if (x < 0) { return Nothing; }
    return Just(x);
}
function main(): i32 {
    match (pick(5)) {
        Just(v)  => { return v; },
        Nothing  => { return -1; }
    }
}`)
	fn := findFunc(prog, "pick")
	if fn == nil {
		t.Fatal("pick not found")
	}
	for _, op := range fn.Ops {
		if op.Kind == OpReturnPair {
			t.Fatalf("Maybe { Nothing, Just(i32) } should NOT be pair-form (wrong variant order):\n%s", prog)
		}
	}
}

// Pointer-shaped payloads on wasm: a pointer is i32 on wasm32,
// so `enum Cell { Filled(string), Empty }` lays flat into the
// `(result i32 i32)` pair-form ABI. The wasm function-side
// emits OpMakeNoneI32 + OpReturnPair instead of an alloc.
func TestLowerUserEnumPointerPayloadIsNotPairFormOnWasm(t *testing.T) {
	// Two-word string ABI on wasm32: a user enum whose
	// payload-carrying variant takes a string is NOT pair-
	// form eligible because the pair-form ABI carries only
	// one i32 payload slot but a string needs two. `f` should
	// stay on the heap-box return shape (OpEnumSentinel for
	// nullary Empty).
	prog := lowerSource(t, `enum Cell { Filled(string), Empty }
function f(): Cell { return Empty; }`)
	if prog.PairForm["f"] {
		t.Errorf("f must not be pair-form on wasm32 (Cell payload includes string):\n%s", prog)
	}
}

// On natives (ptrW=8) the same pointer-shape enum is also
// pair-form-eligible now — the `OpMakeSomeI32` / `OpMakeOkI32`
// / `OpMakeErrI32` native handlers branch on `Op.Width` to
// pick the 16-byte alloc + 8-byte payload store at +8 when
// the payload is pointer-shape. Same nullary `OpMakeNoneI32`
// for the Empty branch.
func TestLowerUserEnumPointerPayloadIsPairFormOnNatives(t *testing.T) {
	prog := lowerSourceWith(t, `enum Cell { Filled(string), Empty }
function f(): Cell { return Empty; }`, 8)
	fn := findFunc(prog, "f")
	if fn == nil {
		t.Fatal("f not found")
	}
	hasNone := false
	for _, op := range fn.Ops {
		if op.Kind == OpMakeNoneI32 {
			hasNone = true
		}
	}
	if !hasNone {
		t.Fatalf("expected OpMakeNoneI32 in f on natives (pointer-shape user enum is now pair-form):\n%s", prog)
	}
}

// `Result[T, E]` with i32-shaped T and E is now pair-form
// eligible alongside `Option[T]`. Function bodies that only
// return `Ok(EXPR)` / `Err(EXPR)` literals lower to
// OpMakeOkI32 / OpMakeErrI32 + OpReturnPair.
func TestLowerResultEmitsPairForm(t *testing.T) {
	prog := lowerSource(t, `function divide(a: i32, b: i32): Result[i32, i32] {
    if (b == 0) { return Err(1); }
    return Ok(a / b);
}`)
	fn := findFunc(prog, "divide")
	if fn == nil {
		t.Fatal("divide not found")
	}
	hasOk := false
	hasErr := false
	hasReturnPair := false
	for _, op := range fn.Ops {
		switch op.Kind {
		case OpMakeOkI32:
			hasOk = true
		case OpMakeErrI32:
			hasErr = true
		case OpReturnPair:
			hasReturnPair = true
		case OpAlloc:
			t.Fatalf("Result pair-form should not OpAlloc:\n%s", prog)
		}
	}
	if !hasOk || !hasErr || !hasReturnPair {
		t.Fatalf("expected OpMakeOkI32 + OpMakeErrI32 + OpReturnPair in divide:\n%s", prog)
	}
}

// Caller-side fast path on a pair-form Result return: a
// `match divide(...)` where `divide` is pair-form-eligible
// drops the heap rebox + OpMatchTag — the call goes through
// OpCallDirectPair, the consumer extracts (tag, payload)
// directly. Mirrors TestLowerMatchOnPairFormCallSkipsRebox
// for Option above.
func TestLowerResultMatchOnPairFormCallSkipsRebox(t *testing.T) {
	prog := lowerSource(t, `function divide(a: i32, b: i32): Result[i32, i32] {
    if (b == 0) { return Err(1); }
    return Ok(a / b);
}
function main(): i32 {
    match (divide(10, 2)) {
        Ok(v)  => { return v; },
        Err(e) => { return 0 - e; }
    }
}`)
	fn := findFunc(prog, "main")
	if fn == nil {
		t.Fatal("main not found")
	}
	hasPairCall := false
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirectPair {
			hasPairCall = true
			break
		}
	}
	if !hasPairCall {
		t.Fatalf("expected OpCallDirectPair in main:\n%s", prog)
	}
	for _, op := range fn.Ops {
		if op.Kind == OpAlloc {
			t.Fatalf("pair-form Result match should skip OpAlloc rebox:\n%s", prog)
		}
		if op.Kind == OpMatchTag {
			t.Fatalf("pair-form Result match should skip OpMatchTag heap-load:\n%s", prog)
		}
	}
}

// Mirror for LetElse: `let Some(v) = pair_form_call() else
// { ... };` also takes the zero-alloc fast path.
func TestLowerLetElseOnPairFormCallSkipsRebox(t *testing.T) {
	prog := lowerSource(t, `function pick(): Option[i32] { return Some(7); }
function main(): i32 {
    let Some(v) = pick() else { return 99; };
    return v;
}`)
	fn := findFunc(prog, "main")
	if fn == nil {
		t.Fatal("main not found")
	}
	hasPairCall := false
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirectPair {
			hasPairCall = true
			break
		}
	}
	if !hasPairCall {
		t.Fatalf("expected OpCallDirectPair in main:\n%s", prog)
	}
	for _, op := range fn.Ops {
		if op.Kind == OpAlloc {
			t.Fatalf("pair-form let-else should skip OpAlloc rebox:\n%s", prog)
		}
		if op.Kind == OpMatchTag {
			t.Fatalf("pair-form let-else should skip OpMatchTag heap-load:\n%s", prog)
		}
	}
}

// A pair-form-eligible function whose only return is a tail
// call to another pair-form function is also pair-form. The
// outer function's heap-box result flows through unchanged, so
// callers can still apply `OpCallDirectPair` consumer-side.
func TestLowerPairFormPropagatesThroughTailCall(t *testing.T) {
	prog := lowerSource(t, `function inner(x: i32): Option[i32] {
    if (x < 0) { return None; }
    return Some(x + 1);
}
function outer(x: i32): Option[i32] { return inner(x); }
function main(): i32 {
    match (outer(2)) {
        Some(v) => { return v; },
        None => { return -1; }
    }
}`)
	// Caller of outer (main) should use OpCallDirectPair —
	// proves outer was marked pair-form despite using only
	// tail calls.
	mainFn := findFunc(prog, "main")
	if mainFn == nil {
		t.Fatal("main not found")
	}
	hasPairCall := false
	for _, op := range mainFn.Ops {
		if op.Kind == OpCallDirectPair && op.Str == "outer" {
			hasPairCall = true
			break
		}
	}
	if !hasPairCall {
		t.Fatalf("expected OpCallDirectPair to outer in main:\n%s", prog)
	}
}

// A tail call to a NON-pair-form function does NOT make the
// caller pair-form — the heap-box result has the right shape
// in memory, but the callees we'd want to opt-in have to opt
// in symmetrically (otherwise the fixpoint never reaches them
// and consumers see arbitrary heap pointers).
func TestLowerTailCallToNonPairFormFnStaysHeapForm(t *testing.T) {
	// `inner` mixes a tail call (the recursion in the false
	// branch) with a literal — the literal makes it pair-form,
	// so what we actually want to pin here is that a tail call
	// to a function whose body escapes the variant (e.g. via a
	// store) is rejected. Use a function that escapes through
	// an arg position.
	prog := lowerSource(t, `function side_effect(o: Option[i32]): Option[i32] { return o; }
function outer(x: i32): Option[i32] { return side_effect(Some(x)); }
function main(): i32 {
    match (outer(2)) {
        Some(v) => { return v; },
        None => { return -1; }
    }
}`)
	mainFn := findFunc(prog, "main")
	if mainFn == nil {
		t.Fatal("main not found")
	}
	for _, op := range mainFn.Ops {
		if op.Kind == OpCallDirectPair && op.Str == "outer" {
			t.Fatalf("outer should NOT be pair-form (tail call into non-pair-form side_effect):\n%s", prog)
		}
	}
}

// A pair-form-eligible function whose only return uses the
// expression-form `if (cond) { Some(x) } else { None }` is
// also pair-form. Each arm constructs a heap-box independently;
// the function-side ABI is unchanged, so the caller can still
// use `OpCallDirectPair` to skip the rebox. The checker's
// `unifyIfArms` flows `Option[i32]` into the bare `None` arm
// so the source type-checks.
func TestLowerPairFormThroughIfExpressionReturn(t *testing.T) {
	prog := lowerSource(t, `function pick(x: i32): Option[i32] {
    return if (x >= 0) { Some(x) } else { None };
}
function main(): i32 {
    match (pick(5)) {
        Some(v) => { return v; },
        None => { return -1; }
    }
}`)
	mainFn := findFunc(prog, "main")
	if mainFn == nil {
		t.Fatal("main not found")
	}
	hasPairCall := false
	for _, op := range mainFn.Ops {
		if op.Kind == OpCallDirectPair && op.Str == "pick" {
			hasPairCall = true
			break
		}
	}
	if !hasPairCall {
		t.Fatalf("expected OpCallDirectPair to pick in main:\n%s", prog)
	}
}

// Nested if-expressions — each leaf must be a pair-form-
// eligible return shape. Picks Ok(x) / Err(0) / Err(1)
// depending on the input.
func TestLowerPairFormThroughNestedIfExpressionReturn(t *testing.T) {
	prog := lowerSource(t, `function classify(x: i32): Result[i32, i32] {
    return if (x > 0) { Ok(x) } else { if (x == 0) { Err(0) } else { Err(1) } };
}
function main(): i32 {
    match (classify(7)) {
        Ok(v)  => { return v; },
        Err(e) => { return 0 - e; }
    }
}`)
	mainFn := findFunc(prog, "main")
	if mainFn == nil {
		t.Fatal("main not found")
	}
	hasPairCall := false
	for _, op := range mainFn.Ops {
		if op.Kind == OpCallDirectPair && op.Str == "classify" {
			hasPairCall = true
			break
		}
	}
	if !hasPairCall {
		t.Fatalf("expected OpCallDirectPair to classify in main:\n%s", prog)
	}
}

// An if-expression whose Else arm doesn't fit the pair-form
// shape (e.g. forwards a heap-form parameter) blocks pair-form
// detection — the analysis must reject the whole function.
func TestLowerIfExpressionWithEscapingArmStaysHeapForm(t *testing.T) {
	prog := lowerSource(t, `function side_effect(o: Option[i32]): Option[i32] { return o; }
function pick(x: i32, fallback: Option[i32]): Option[i32] {
    return if (x < 0) { side_effect(fallback) } else { Some(x) };
}
function main(): i32 {
    match (pick(5, None)) {
        Some(v) => { return v; },
        None => { return -1; }
    }
}`)
	mainFn := findFunc(prog, "main")
	if mainFn == nil {
		t.Fatal("main not found")
	}
	for _, op := range mainFn.Ops {
		if op.Kind == OpCallDirectPair && op.Str == "pick" {
			t.Fatalf("pick should NOT be pair-form (ternary arm escapes via non-pair-form side_effect):\n%s", prog)
		}
	}
}

// `Option[string]` on wasm is now pair-form-eligible — pointer
// payloads lay flat into an i32 slot on wasm32, so the
// function-side emits OpMakeNoneI32 (the canonical tag-1 op)
// instead of an OpEnumSentinel + heap-box path.
func TestLowerOptionPointerPayloadIsNotPairFormOnWasm(t *testing.T) {
	// Two-word string ABI on wasm32: `Option[string]` is not
	// pair-form eligible (the one-i32-payload-slot pair-form
	// ABI can't carry a two-word string). `f` should fall back
	// to the heap-box return shape.
	prog := lowerSource(t, `function f(): Option[string] { return None; }`)
	if prog.PairForm["f"] {
		t.Errorf("f must not be pair-form on wasm32 (Option[string] payload needs two-word ABI):\n%s", prog)
	}
}

// On natives (ptrW=8) `Option[string]` is now pair-form too:
// the maker ops carry `Op.Width = WidthPtr` so the heap-box
// layout (alloc 16, payload at +8 as 8-byte store) matches
// `payloadLayout(Option[string])` and the existing match-side
// readers find the payload at the same offset.
func TestLowerOptionPointerPayloadIsPairFormOnNatives(t *testing.T) {
	prog := lowerSourceWith(t, `function f(): Option[string] { return None; }`, 8)
	mustContainOp(t, prog, "f", OpMakeNoneI32)
}

func TestLowerLenOnStringEmitsOpStrLen(t *testing.T) {
	prog := lowerSource(t, `function f(s: string): i32 { return s.len(); }`)
	mustContainOp(t, prog, "f", OpStrLen)
	// The old inline shape (const 4; sub; load) must NOT appear
	// for the string path — if it does, the rewrite slipped and
	// SSO would have to patch every backend instead of just the
	// OpStrLen handler.
	fn := findFunc(prog, "f")
	for i := 0; i+2 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == OpConstI32 && fn.Ops[i].I32 == 4 &&
			fn.Ops[i+1].Kind == OpSub && fn.Ops[i+2].Kind == OpLoad {
			t.Fatalf("string len() lowering still emits the open-coded const-4/sub/load shape:\n%s", prog)
		}
	}
}

// `len(f(...))` where `f` returns a string must route through
// OpStrLen so the SSO seam handles the inline / heap branch.
// Without an *ast.Call case in `exprType`, the lowering falls
// through to the array-shape `[ptr - 4]; load` fallback, which
// traps on inline-form strings produced by string-returning
// helpers — most importantly `int_to_string`, whose 1..3-digit
// outputs cascade through `$string_from_bytes_unchecked`'s inline path.
// Pin the lowering here so the regression can't slip back in.
func TestLowerLenOnStringCallEmitsOpStrLen(t *testing.T) {
	prog := lowerSource(t, `function g(): string { return "abcd"; }
function f(n: i32): i32 { return (g()).len(); }`)
	mustContainOp(t, prog, "f", OpStrLen)
	fn := findFunc(prog, "f")
	for i := 0; i+2 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == OpConstI32 && fn.Ops[i].I32 == 4 &&
			fn.Ops[i+1].Kind == OpSub && fn.Ops[i+2].Kind == OpLoad {
			t.Fatalf("(call-returning-string).len() still emits the open-coded const-4/sub/load shape:\n%s", prog)
		}
	}
}

// `len(if cond { a } else { b })` where the unified arm type is
// `string` must route through OpStrLen so the SSO seam handles
// the inline / heap branch. Without an *ast.IfExpr case in
// `exprType`, the lowering falls through to the array-shape
// `[ptr - 4]; load` fallback, which traps when one of the arms
// produces an inline-form string (e.g.
// `if cond { int_to_string(n) } else { s }`).
func TestLowerLenOnStringIfExprEmitsOpStrLen(t *testing.T) {
	prog := lowerSource(t, `function f(cond: boolean, s: string): i32 {
		return (if (cond) { s } else { "fallback" }).len();
	}`)
	mustContainOp(t, prog, "f", OpStrLen)
	fn := findFunc(prog, "f")
	for i := 0; i+2 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == OpConstI32 && fn.Ops[i].I32 == 4 &&
			fn.Ops[i+1].Kind == OpSub && fn.Ops[i+2].Kind == OpLoad {
			t.Fatalf("(IfExpr-returning-string).len() still emits the open-coded const-4/sub/load shape:\n%s", prog)
		}
	}
}

// `len(match e { ... })` parallels the IfExpr case — every arm
// body shares a unified type, so recursing on the first arm
// body that resolves is sufficient.
func TestLowerLenOnStringMatchExprEmitsOpStrLen(t *testing.T) {
	src := `enum Color { R, G, B }
		function pick(c: Color): string {
			return match (c) {
				R => "red",
				G => "grn",
				B => "blu"
			};
		}
		function f(c: Color): i32 { return (pick(c)).len(); }`
	prog := lowerSource(t, src)
	mustContainOp(t, prog, "f", OpStrLen)
}

// `len(<string literal>)` is still folded to a compile-time const
// — the OpStrLen path is only for non-literal strings.
func TestLowerLenOnStringLiteralFolds(t *testing.T) {
	prog := lowerSource(t, `function f(): i32 { return ("hello").len(); }`)
	fn := findFunc(prog, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpStrLen {
			t.Errorf("len of a string literal must fold to const, not OpStrLen:\n%s", prog)
		}
	}
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 5 {
			return
		}
	}
	t.Errorf("expected const 5 for (\"hello\").len():\n%s", prog)
}

// `len(a[i])` where `a` is a string array must route through
// OpStrLen — same SSO seam reason as `len(s)`. Latent bug:
// before exprType learned about *ast.Index, the dispatch fell
// to the array-shape `[ptr - 4]; load` fallback, which traps
// for inline-form strings produced by $args / $string_from_bytes_unchecked
// / $__str_concat. Pin the lowering here so the fallback can't
// silently slip back in.
func TestLowerLenOnStringArrayIndexEmitsOpStrLen(t *testing.T) {
	prog := lowerSource(t, `function f(a: string[]): i32 { return a[0].len(); }`)
	mustContainOp(t, prog, "f", OpStrLen)
	fn := findFunc(prog, "f")
	for i := 0; i+2 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == OpConstI32 && fn.Ops[i].I32 == 4 &&
			fn.Ops[i+1].Kind == OpSub && fn.Ops[i+2].Kind == OpLoad {
			t.Fatalf("(string-array[i]).len() still emits the open-coded const-4/sub/load shape:\n%s", prog)
		}
	}
}

// `len(arr)` keeps the open-coded shape — arrays may diverge
// from strings in a future layout change, and routing them
// through OpStrLen would conflate the two.
func TestLowerLenOnArrayKeepsInlineShape(t *testing.T) {
	prog := lowerSource(t, `function f(xs: i32[]): i32 { return xs.len(); }`)
	if hasOp(prog, "f", OpStrLen) {
		t.Errorf("array len() must not lower to OpStrLen:\n%s", prog)
	}
	mustContainOp(t, prog, "f", OpLoad)
}

// `lit == lit` folds at compile time to a const; the runtime
// OpStrEq only fires when at least one side is non-literal.
func TestLowerStringEqualityFoldsLiterals(t *testing.T) {
	prog := lowerSource(t, `function f(): boolean { return "a" == "b"; }`)
	if hasOp(prog, "f", OpStrEq) {
		t.Errorf("lit == lit must fold; OpStrEq must not appear:\n%s", prog)
	}
	mustContainOp(t, prog, "f", OpConstI32)
}

func TestLowerStringEqualityIdentVsLitShortCircuits(t *testing.T) {
	prog := lowerSource(t, `function f(s: string): boolean { return s == "ok"; }`)
	// Length comparison emits a const for the literal length...
	mustContainOp(t, prog, "f", OpConstI32)
	// ...and falls back to OpStrEq only when lengths match.
	mustContainOp(t, prog, "f", OpStrEq)
	// The if/else split is materialised as OpIf in the IR.
	mustContainOp(t, prog, "f", OpIf)
}

// Closure conversion runs as a precondition of Lower, so a nested
// function that captures an outer var should appear in the IR's
// Funcs list with a generated `__closure_*` name and the original
// def site should emit OpMakeClosure.
func TestLowerNestedFunctionHoists(t *testing.T) {
	prog := lowerSource(t, `function outer(): i32 {
		var n: i32 = 7;
		function inner(): i32 { return n + 1; }
		return inner();
	}`)
	// Two user-facing functions in the IR: the outer and the hoisted
	// inner. A MakeClosure'd closure also gets a generated
	// `__closure_drop_<name>` thunk (its pair carries a drop-fn
	// pointer for generic env reclamation), so filter those out.
	var userFuncs []*Func
	for _, fn := range prog.Funcs {
		if !strings.HasPrefix(fn.Name, "__closure_drop_") {
			userFuncs = append(userFuncs, fn)
		}
	}
	if len(userFuncs) != 2 {
		t.Fatalf("expected 2 funcs (outer + hoisted inner), got %d:\n%s", len(userFuncs), prog)
	}
	var hoisted *Func
	for _, fn := range userFuncs {
		if strings.HasPrefix(fn.Name, "__closure_") {
			hoisted = fn
		}
	}
	if hoisted == nil {
		t.Fatalf("expected a hoisted __closure_* function, got:\n%s", prog)
	}
	// The hoisted function carries the synthetic env parameter as its
	// last param so call_indirect through the funcref table is uniform.
	if last := hoisted.Params[len(hoisted.Params)-1].Name; last != "__env" {
		t.Errorf("hoisted function's last param = %q, want __env", last)
	}
	// The outer's def site is now `var inner = MakeClosure{...}`, so
	// outer's ops contain OpMakeClosure.
	mustContainOp(t, prog, "outer", OpMakeClosure)
}

// Captures lower to env-relative loads inside the hoisted body: the
// IR walks `local.get $__env; const offset; add; load`.
func TestLowerCaptureRefIsEnvRelativeLoad(t *testing.T) {
	prog := lowerSource(t, `function outer(): i32 {
		var n: i32 = 5;
		function inner(): i32 { return n; }
		return inner();
	}`)
	var hoisted *Func
	for _, fn := range prog.Funcs {
		if strings.HasPrefix(fn.Name, "__closure_") && !strings.HasPrefix(fn.Name, "__closure_drop_") {
			hoisted = fn
		}
	}
	if hoisted == nil {
		t.Fatalf("hoisted function missing:\n%s", prog)
	}
	// We expect the body to load the env, push the offset, add, then
	// load the captured word — so OpAdd + OpLoad must both appear.
	mustContainOp(t, prog, hoisted.Name, OpAdd)
	mustContainOp(t, prog, hoisted.Name, OpLoad)
	// And the env parameter must be referenced via OpLoadLocal at its
	// param-slot index (the last param's slot).
	envSlot := int32(len(hoisted.Params) - 1)
	hasEnvLoad := false
	for _, op := range hoisted.Ops {
		if op.Kind == OpLoadLocal && op.I32 == envSlot {
			hasEnvLoad = true
		}
	}
	if !hasEnvLoad {
		t.Errorf("hoisted body never loads the __env slot %d:\n%s", envSlot, prog)
	}
}

// Every function's op list must be a balanced sequence of structured
// scopes: each Block/Loop/If is matched by an End, and the depth is
// 0 at function entry and exit. This is the precondition any
// structured-control-flow target (WAT, etc.) relies on.
func TestStructuredControlFlowIsBalanced(t *testing.T) {
	prog := lowerSource(t, `function f(n: i32): i32 {
		var sum: i32 = 0;
		for (var i: i32 = 0; i < n; i = i + 1) {
			if (i == 5) { break; }
			if (i == 7) { continue; }
			match (i) {
				1 => { sum = sum + 10; },
				2 => { sum = sum + 10; },
				3 => { sum = sum + 30; },
				_ => { sum = sum + 1; }
			}
		}
		while (sum > 100) {
			sum = sum - 1;
		}
		return if (sum > 0) { sum } else { 0 };
	}`)
	for _, fn := range prog.Funcs {
		depth := 0
		for i, op := range fn.Ops {
			switch op.Kind {
			case OpBlock, OpLoop, OpIf:
				depth++
			case OpEnd:
				depth--
				if depth < 0 {
					t.Fatalf("%s: op %d (%s): depth went negative", fn.Name, i, op.Kind)
				}
			case OpBr, OpBrIf:
				if op.I32 < 0 || op.I32 > int32(depth-1) {
					t.Errorf("%s: op %d (%s %d): branch depth out of range (depth=%d)",
						fn.Name, i, op.Kind, op.I32, depth)
				}
			}
		}
		if depth != 0 {
			t.Errorf("%s: ended at depth %d, want 0", fn.Name, depth)
		}
	}
}

// ScratchTypes records the type of every synthetic slot the lowering pass
// conjured beyond the user-visible params + locals — codegen needs
// the count to declare matching WAT locals.
func TestLowerNumScratchTracked(t *testing.T) {
	// A program with no synthetic helpers: ScratchTypes is empty.
	pPlain := lowerSource(t, `function f(a: i32, b: i32): i32 {
		var x: i32 = a + b;
		return x;
	}`)
	if got := len(pPlain.Funcs[0].ScratchTypes); got != 0 {
		t.Errorf("plain function: ScratchTypes.len() = %d, want 0", got)
	}
	// A program using array, struct, and match helpers should report
	// at least one scratch slot per helper kind.
	pHelpers := lowerSource(t, `struct P { x: i32 }
		function f(n: i32): i32 {
			var a: i32[] = [1, 2, 3];
			var p: P = P { x: 5 };
			match (n) { 0 => { return 0; }, _ => { return 1; } }
		}`)
	if got := len(pHelpers.Funcs[0].ScratchTypes); got < 3 {
		t.Errorf("helper-heavy function: ScratchTypes.len() = %d, want >= 3", got)
	}
}

// OpCallIndirect carries the static signature of the function-typed
// local so codegen can resolve a `(type $tN)` clause without tracing
// back through the preceding OpLoadLocal.
func TestLowerCallIndirectCarriesSig(t *testing.T) {
	prog := lowerSource(t, `function add(a: i32, b: i32): i32 { return a + b; }
		function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
			return f(a, b);
		}`)
	apply := findFunc(prog, "apply")
	if apply == nil {
		t.Fatal("apply not found")
	}
	for _, op := range apply.Ops {
		if op.Kind != OpCallIndirect {
			continue
		}
		if op.Sig() == nil {
			t.Fatalf("OpCallIndirect.Sig is nil:\n%s", prog)
		}
		if len(op.Sig().Params) != 2 {
			t.Errorf("Sig.Params = %d, want 2", len(op.Sig().Params))
		}
		return
	}
	t.Fatalf("OpCallIndirect not found:\n%s", prog)
}

// Lowering stamps each op with the source position of the AST node
// it came from, so consumers can emit per-statement DWARF .loc /
// debug-line entries. The recorded line tracks the surface syntax
// (e.g. operands inside an expression statement carry the line of
// that expression).
func TestLowerStampsSourcePositions(t *testing.T) {
	src := `function f(a: i32, b: i32): i32 {
		var x: i32 = a + b;
		return x;
	}`
	prog := lowerSource(t, src)
	if len(prog.Funcs) != 1 {
		t.Fatalf("got %d funcs", len(prog.Funcs))
	}
	fn := prog.Funcs[0]
	// First op evaluates `a` (line 2 in the source). Last meaningful
	// op is the OpReturn from `return x;` on line 3.
	if got := fn.Ops[0].Pos.Line; got != 2 {
		t.Errorf("first op (load `a`) at line %d, want 2", got)
	}
	var lastReturn int = -1
	for i, op := range fn.Ops {
		if op.Kind == OpReturn {
			lastReturn = i
		}
	}
	if lastReturn < 0 {
		t.Fatalf("no OpReturn found:\n%s", prog)
	}
	if got := fn.Ops[lastReturn].Pos.Line; got != 3 {
		t.Errorf("OpReturn at line %d, want 3", got)
	}
}

// MakeClosure carries the hoisted function name and a capture count
// so codegen can resolve both the funcref-table index and the env
// block size.
func TestLowerMakeClosureCarriesNameAndCount(t *testing.T) {
	prog := lowerSource(t, `function outer(): i32 {
		var a: i32 = 1;
		var b: i32 = 2;
		function inner(): i32 { return a + b; }
		return inner();
	}`)
	outer := findFunc(prog, "outer")
	if outer == nil {
		t.Fatal("outer not found")
	}
	var mc *Op
	for i := range outer.Ops {
		if outer.Ops[i].Kind == OpMakeClosure {
			mc = &outer.Ops[i]
		}
	}
	if mc == nil {
		t.Fatalf("OpMakeClosure missing in outer:\n%s", prog)
	}
	if !strings.HasPrefix(mc.Str, "__closure_inner_") {
		t.Errorf("MakeClosure name = %q, want __closure_inner_*", mc.Str)
	}
	if mc.I32 != 2 {
		t.Errorf("MakeClosure capture count = %d, want 2", mc.I32)
	}
}

// Arithmetic / bitwise identities at the IR builder elide the
// trivial side. Tests use a parameter for the non-literal side
// so the all-const fold path doesn't preempt.
func TestLowerArithIdentities(t *testing.T) {
	cases := []struct {
		src      string
		bannedOp OpKind // op that the fold should remove
		desc     string
	}{
		{`function f(x: i32): i32 { return x + 0; }`, OpAdd, "x + 0"},
		{`function f(x: i32): i32 { return 0 + x; }`, OpAdd, "0 + x"},
		{`function f(x: i32): i32 { return x - 0; }`, OpSub, "x - 0"},
		{`function f(x: i32): i32 { return x * 1; }`, OpMul, "x * 1"},
		{`function f(x: i32): i32 { return 1 * x; }`, OpMul, "1 * x"},
		{`function f(x: i32): i32 { return x | 0; }`, OpOr, "x | 0"},
		{`function f(x: i32): i32 { return x ^ 0; }`, OpXor, "x ^ 0"},
		{`function f(x: i32): i32 { return x & -1; }`, OpAnd, "x & -1"},
		{`function f(x: i32): i32 { return x << 0; }`, OpShl, "x << 0"},
		{`function f(x: i32): i32 { return x >> 0; }`, OpShrS, "x >> 0"},
	}
	for _, tc := range cases {
		prog := lowerSource(t, tc.src)
		if hasOp(prog, "f", tc.bannedOp) {
			t.Errorf("%s should fold; %s must not appear:\n%s", tc.desc, tc.bannedOp, prog)
		}
	}
}

// Self-identity folds collapse `x op x` to a known constant
// or the operand itself. Inspired by Cranelift's icmp.isle /
// arithmetic.isle. Restricted to plain identifiers so we
// don't double-evaluate side effects.
func TestLowerSelfIdentityFolds(t *testing.T) {
	cases := []struct {
		op       string
		bannedOp OpKind
		desc     string
	}{
		{"-", OpSub, "x - x"},
		{"^", OpXor, "x ^ x"},
		{"|", OpOr, "x | x"},
		{"&", OpAnd, "x & x"},
		{"==", OpEq, "x == x"},
		{"!=", OpNe, "x != x"},
		{"<", OpLtS, "x < x"},
		{"<=", OpLeS, "x <= x"},
		{">", OpGtS, "x > x"},
		{">=", OpGeS, "x >= x"},
	}
	for _, tc := range cases {
		retType := "i32"
		if tc.op == "==" || tc.op == "!=" || tc.op == "<" ||
			tc.op == "<=" || tc.op == ">" || tc.op == ">=" {
			retType = "boolean"
		}
		src := "function f(x: i32): " + retType + " { return x " + tc.op + " x; }"
		prog := lowerSource(t, src)
		if hasOp(prog, "f", tc.bannedOp) {
			t.Errorf("%s should fold; %s must not appear:\n%s", tc.desc, tc.bannedOp, prog)
		}
	}
}

// `x * 2^k` strength-reduces to `x << k` (k > 0). For
// non-power-of-two multipliers the OpMul stays.
func TestLowerArithStrengthReducesMulPow2(t *testing.T) {
	cases := []struct {
		src      string
		wantOp   OpKind
		bannedOp OpKind
		desc     string
	}{
		{`function f(x: i32): i32 { return x * 2; }`, OpShl, OpMul, "x * 2"},
		{`function f(x: i32): i32 { return x * 4; }`, OpShl, OpMul, "x * 4"},
		{`function f(x: i32): i32 { return x * 16; }`, OpShl, OpMul, "x * 16"},
		{`function f(x: i32): i32 { return 8 * x; }`, OpShl, OpMul, "8 * x"},
		{`function f(x: i32): i32 { return x * 3; }`, OpMul, OpShl, "x * 3"},
		{`function f(x: i32): i32 { return x * 7; }`, OpMul, OpShl, "x * 7"},
	}
	for _, tc := range cases {
		prog := lowerSource(t, tc.src)
		if hasOp(prog, "f", tc.bannedOp) {
			t.Errorf("%s: %s should not appear:\n%s", tc.desc, tc.bannedOp, prog)
		}
		if !hasOp(prog, "f", tc.wantOp) {
			t.Errorf("%s: expected %s in lowered IR:\n%s", tc.desc, tc.wantOp, prog)
		}
	}
}

// callsDirect reports whether the named function's ops contain an
// OpCallDirect to callee.
func callsDirect(p *Program, fnName, callee string) bool {
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if isNamedCallKind(op.Kind) && op.Str == callee {
				return true
			}
		}
	}
	return false
}

func funcExists(p *Program, name string) bool {
	for _, fn := range p.Funcs {
		if fn.Name == name {
			return true
		}
	}
	return false
}

// TestLowerGenericEnumStructFieldDrop verifies a heap-boxed generic-enum
// instantiation used as a struct field (`Holder { b: Option[Item] }`)
// routes its drop through a per-instantiation deep-drop fn rather than
// the flat one-level dec that leaks the box + payload. Holder is nested
// inside Outer so it reclaims through a GENERATED __drop_struct_Holder
// (appendChildDrop), exercising the registry threaded through the gen
// functions — not just the builder's inline dropStructField. That body
// must call __drop_enum_<mangled>, and the worklist must have generated
// that fn from the stashed substituted decl.
func TestLowerGenericEnumStructFieldDrop(t *testing.T) {
	p := lowerSourceWith(t, `struct Item { xs: i32[] }
struct Holder { b: Option[Item], n: i32 }
struct Outer { h: Holder, tag: i32 }
function build(): i32 {
    var o: Outer = Outer { h: Holder { b: Some(Item { xs: [1, 2] }), n: 3 }, tag: 4 };
    var got: i32 = 0;
    match (o.h.b) { Some(it) => { got = it.xs[1]; }, None => { got = 0; } }
    return got;
}`, 8)
	const drop = "__drop_enum_Option_LB_Item_RB_"
	if !funcExists(p, drop) {
		t.Fatalf("expected generated %s fn (generic-enum-instantiation deep-drop):\n%s", drop, p)
	}
	if !funcExists(p, "__drop_struct_Holder") {
		t.Fatalf("expected generated __drop_struct_Holder (Holder nested in Outer):\n%s", p)
	}
	if !callsDirect(p, "__drop_struct_Holder", drop) {
		t.Errorf("expected __drop_struct_Holder to deep-drop its Option[Item] field via %s:\n%s", drop, p)
	}
}

// TestLowerGenericEnumScalarFieldFreesBox pins that a SCALAR generic
// instantiation as a field — Option[i32] — DOES mint a per-instantiation
// drop fn, so its box is reclaimed.
//
// Such a field is NOT "pair-form, no heap box", and a box_free on it does
// not corrupt. The measurement is direct:
// `struct H { a: i32, n: i32 }` built in a loop allocates 1 block per
// iteration; adding an Option[i32] field makes it 2. The Option field is
// separately boxed. With the drop suppressed it leaked 16 bytes per
// construction, linear and unbounded (#5917) — including for this test's
// own `Some(7)` literal shape, measured at allocs=200 frees=100 live=1600
// over 100 iterations.
//
// "pair-form" is a per-FUNCTION return ABI (findPairFormFuncs, keyed by
// function name) describing how a callee hands an Option back. It says
// nothing about a field's representation, and reading it as a type-level
// property is what produced both the wrong comment and this wrong test.
//
// The corruption the old test feared does not occur: this program returns
// 7 and reports allocs=2 frees=2 live_bytes=0 end-to-end, matching the
// interpreter. Runtime coverage lives in
// TestX86_64/Arm64LeakCheckEnumStringPayloadBox's scalar-nested-in-struct
// and scalar-nested-in-tuple fixtures, which assert value AND balance.
func TestLowerGenericEnumScalarFieldFreesBox(t *testing.T) {
	p := lowerSourceWith(t, `struct Holder { b: Option[i32], n: i32 }
function build(): i32 {
    var h: Holder = Holder { b: Some(7), n: 3 };
    var got: i32 = 0;
    match (h.b) { Some(v) => { got = v; }, None => { got = 0; } }
    return got;
}`, 8)
	if !funcExists(p, "__drop_enum_Option_LB_i32_RB_") && !funcExists(p, "__drop_enum_Option_LB_number_RB_") {
		t.Errorf("scalar Option[i32] field must mint a deep-drop fn so its box is freed:\n%s", p)
	}
}

// TestLowerStringConcatLocalReclaim verifies a fresh owned string local
// (a concat result — always a fresh headered heap buffer) reclaims via
// __fern_str_dec at its last reference on wasm (ptrW=4). Aliases / views
// / literals are tainted and skipped, so the dec only ever sees fresh
// owned strings.
func TestLowerStringConcatLocalReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var pre: string = "v";
    var s: string = pre + "x";
    return s.len();
}`, 4)
	if !callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("expected fresh concat string local to reclaim via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringAliasReclaim verifies that aliasing a fresh owned string
// (var s2 = s1, where s1 is a concat result — a headered heap buffer)
// retains the shared buffer via the two-word __fern_str_inc, and that both
// locals reach __fern_str_dec at exit. The dec's rc==1 / is-unique gate
// frees the buffer exactly once (the first dec sees rc=2 and only
// decrements; the second sees rc=1 and frees).
func TestLowerStringAliasReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var s1: string = "a" + "b";
    var s2: string = s1;
    return s1.len() + s2.len();
}`, 4)
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("expected aliased fresh string to retain via __fern_str_inc:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("expected aliased fresh string locals to reclaim via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringAliasIncIsUniform verifies that aliasing a string read
// out of a container (here, an Index into a string array) emits
// __fern_str_inc — string retain is now UNCONDITIONAL (the old
// eligibility gate that skipped non-fresh sources is gone). Views no
// longer exist (args()/env() copy into owned strings), so a borrowed
// read must co-own the buffer: without the inc, a later container drop
// would free it out from under the alias. Inline / literal strings make
// the inc a runtime no-op, so the unconditional emit is safe.
func TestLowerStringAliasIncIsUniform(t *testing.T) {
	p := lowerSourceWith(t, `function build(xs: string[]): i32 {
    var v: string = xs[0];
    var v2: string = v;
    return v.len() + v2.len();
}`, 4)
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("aliasing a container-read string must emit __fern_str_inc (uniform retain):\n%s", p)
	}
}

// TestLowerStringReclaimOnNative verifies string reclamation now fires on a
// native ptrW (8) too: a fresh owned string local (`s = pre + "x"`) is
// free-eligible and drops via the freeing __fern_str_dec at its last
// reference. Gating this to the two-word ABIs (wasm / arm64) leaks native
// heap strings, so computeFreeEligible admits native single-word string
// locals and __fern_str_dec frees the box at rc==1 with
// the size-class-matched length+1 payload. See docs/IR-SELFCOMPILE-OOM.
func TestLowerStringReclaimOnNative(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var pre: string = "v";
    var s: string = pre + "x";
    return s.len();
}`, 8)
	if !callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("native (ptrW=8) must reclaim owned string locals via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringPassedToUserFnNotReclaimedNative pins the #4174 follow-up: a
// native single-word STRING local passed as an argument to a USER function may
// be RETAINED by the callee (stored into a container it returns — the
// intraprocedural escape analysis can't see it), so it must NOT be reclaimed
// caller-side (freeing it would dangle the retained copy — a nested
// control-flow miscompile observed in a self-host codegen helper). `keep(s)` is a user
// call, so `s` is tainted and no __fern_str_dec fires for it. (A builtin borrow
// like `s.len()` — TestLowerStringReclaimOnNative — still reclaims: the method
// receiver Args[0] is skipped.)
func TestLowerStringPassedToUserFnNotReclaimedNative(t *testing.T) {
	p := lowerSourceWith(t, `function keep(s: string): i32 { return 0; }
function build(): i32 {
    var s: string = "a" + "b";
    return keep(s);
}`, 8)
	if callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("native: a string moved into a user-fn arg must NOT be reclaimed caller-side (retained-copy UAF):\n%s", p)
	}
}

// TestLowerStringStructFieldReclaim verifies a string struct field is
// reclaimed. A top-level struct LOCAL reclaims inline at its last
// reference (the emitDec struct branch), so `build` itself dec's the
// string field via the two-word __fern_str_dec; the alias-shaped field
// initialiser (`Holder { name: s }`) retains via __fern_str_inc on
// construction so the dec balances. Gated wasm (ptrW=4).
func TestLowerStringStructFieldReclaim(t *testing.T) {
	p := lowerSourceWith(t, `struct Holder { name: string }
function build(s: string): i32 {
    var h: Holder = Holder { name: s };
    return h.name.len();
}`, 4)
	// The reclamation is OUTLINED: `build` calls the generated drop fn and
	// that fn dec's the field. Assert both halves — the pair is a stronger
	// statement than the old single "build calls __fern_str_dec", which
	// could not distinguish the field's dec from any other string dec in
	// the function.
	if !callsDirect(p, "build", "__drop_struct_Holder") {
		t.Errorf("expected the struct local to reclaim via __drop_struct_Holder:\n%s", p)
	}
	if !callsDirect(p, "__drop_struct_Holder", "__fern_str_dec") {
		t.Errorf("expected __drop_struct_Holder to dec its string field via __fern_str_dec:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("expected alias-shaped string field init to retain via __fern_str_inc:\n%s", p)
	}
}

// TestLowerStringNestedStructFieldReclaim verifies the generated
// __drop_struct_<T> path: an Inner struct nested as a field of Outer
// reclaims through Outer's drop recursing into __drop_struct_Inner,
// which dec's Inner's string field via __fern_str_dec.
func TestLowerStringNestedStructFieldReclaim(t *testing.T) {
	p := lowerSourceWith(t, `struct Inner { name: string }
struct Outer { inner: Inner }
function build(s: string): i32 {
    var o: Outer = Outer { inner: Inner { name: s } };
    return o.inner.name.len();
}`, 4)
	if !callsDirect(p, "__drop_struct_Inner", "__fern_str_dec") {
		t.Errorf("expected __drop_struct_Inner to reclaim its string field via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringStructFieldReclaimOnNative verifies the native (single-
// word x86-64) struct drop reclaims its string field via __fern_str_dec —
// the field is retained on construction (field-init emitAliasInc →
// __fern_rc_inc when the initialiser aliases, or moved in when fresh-
// owned), so __drop_struct_<N> freeing the buffer at the field's rc==1 is
// exactly balanced. (Phase 1e-strings native struct-field slice.)
func TestLowerStringStructFieldReclaimOnNative(t *testing.T) {
	p := lowerSourceWith(t, `struct Holder { name: string }
function build(s: string): i32 {
    var h: Holder = Holder { name: s };
    return h.name.len();
}`, 8)
	if !callsDirect(p, "__drop_struct_Holder", "__fern_str_dec") {
		t.Errorf("native (ptrW=8) struct drop must reclaim its string field via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringStructFieldReclaimOnArm64TwoWord verifies Slice 3 on
// arm64: a struct local with a string field reclaims the field via the
// two-word __fern_str_dec at scope exit, same shape as wasm. The
// alias-inc on construction balances against the dec — both fire on
// the two-word string ABI gate (UseTwoWordStrings), so wasm and
// arm64-TwoWordOverride share the same Slice 3 codegen. arm64 single-
// word (the !TwoWordOverride path) keeps the native rc_dec path.
func TestLowerStringStructFieldReclaimOnArm64TwoWord(t *testing.T) {
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()
	p := lowerSourceWith(t, `struct Holder { name: string }
function build(s: string): i32 {
    var h: Holder = Holder { name: s };
    return h.name.len();
}`, 8)
	if !callsDirect(p, "build", "__drop_struct_Holder") {
		t.Errorf("arm64 two-word: expected the struct local to reclaim via __drop_struct_Holder:\n%s", p)
	}
	if !callsDirect(p, "__drop_struct_Holder", "__fern_str_dec") {
		t.Errorf("arm64 two-word: expected __drop_struct_Holder to dec its string field via __fern_str_dec:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("arm64 two-word: expected alias-shaped string field init to retain via __fern_str_inc:\n%s", p)
	}
}

// TestLowerStringNestedStructFieldReclaimOnArm64TwoWord locks the
// generated __drop_struct_<T> path on arm64 too: an Inner struct
// nested as a field of Outer reclaims through Outer's drop recursing
// into __drop_struct_Inner, which dec's Inner's string field via
// __fern_str_dec under the two-word ABI.
func TestLowerStringNestedStructFieldReclaimOnArm64TwoWord(t *testing.T) {
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()
	p := lowerSourceWith(t, `struct Inner { name: string }
struct Outer { inner: Inner }
function build(s: string): i32 {
    var o: Outer = Outer { inner: Inner { name: s } };
    return o.inner.name.len();
}`, 8)
	if !callsDirect(p, "__drop_struct_Inner", "__fern_str_dec") {
		t.Errorf("arm64 two-word: expected __drop_struct_Inner to reclaim its string field via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringTupleElemReclaim verifies a string tuple element is
// reclaimed: the owned tuple local's deep-drop dec's the element via the
// two-word __fern_str_dec at the tuple's last reference, and the alias-
// shaped element initialiser retains via __fern_str_inc. Gated wasm.
func TestLowerStringTupleElemReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(s: string): i32 {
    var t: (string, i32) = (s, 1);
    return t.0.len();
}`, 4)
	if !callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("expected tuple local reclamation to dec its string element via __fern_str_dec:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("expected alias-shaped string tuple element init to retain via __fern_str_inc:\n%s", p)
	}
}

// TestLowerStringTupleDestructureDup verifies destructuring a string out
// of a tuple dups it via __fern_str_inc so the binding co-owns the
// buffer alongside the tuple box (without it, the tuple's deep-drop
// __fern_str_dec would free the buffer under the live binding → UAF).
func TestLowerStringTupleDestructureDup(t *testing.T) {
	p := lowerSourceWith(t, `function build(s: string): i32 {
    var t: (string, i32) = (s, 1);
    var (a, b) = t;
    return a.len();
}`, 4)
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("expected destructure dup of a string element to emit __fern_str_inc:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("expected tuple + binding string reclamation via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringTupleElemReclaimOnNative is the single-word (ptrW=8)
// sibling of the test above: the exit sweep's inline tuple arm frees the
// element buffer with __fern_str_dec, matching what genTupleDropFn already
// emits for a nested tuple of the same shape. A bare __fern_rc_dec only
// decrements, so the buffer's count reaches 0 and nothing is reclaimed.
func TestLowerStringTupleElemReclaimOnNative(t *testing.T) {
	p := lowerSourceWith(t, `function build(s: string): i32 {
    var t: (string, i32) = (s, 1);
    return t.0.len();
}`, 8)
	if !callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("native (ptrW=8) tuple drop must reclaim its string element via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringArrayElemReclaim verifies a string[] local reclaims its
// element strings: the array drop routes through __fern_drop_arr_str
// (which walks the two-word elements calling __fern_str_dec, then frees
// the buffer) rather than the buffer-only __fern_arr_dec. Gated wasm.
func TestLowerStringArrayElemReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(a: string, b: string): i32 {
    var arr: string[] = [a, b];
    return arr[0].len() + arr[1].len();
}`, 4)
	if !callsDirect(p, "build", "__fern_drop_arr_str") {
		t.Errorf("expected string[] local reclamation via __fern_drop_arr_str:\n%s", p)
	}
}

// TestLowerStringArrayInStructFieldReclaim verifies a string[] struct
// field also routes through __fern_drop_arr_str when the struct drops.
func TestLowerStringArrayInStructFieldReclaim(t *testing.T) {
	p := lowerSourceWith(t, `struct Tags { items: string[] }
function build(a: string): i32 {
    var t: Tags = Tags { items: [a] };
    return t.items[0].len();
}`, 4)
	if !callsDirect(p, "build", "__drop_struct_Tags") {
		t.Errorf("expected the struct local to reclaim via __drop_struct_Tags:\n%s", p)
	}
	if !callsDirect(p, "__drop_struct_Tags", "__fern_drop_arr_str") {
		t.Errorf("expected __drop_struct_Tags to reclaim its string[] field via __fern_drop_arr_str:\n%s", p)
	}
}

// TestLowerStringArrayElemReclaimOnNative verifies the native (single-word
// x86-64) string[] drop reclaims its element strings via the freeing
// __fern_drop_arr_str (per-element __fern_str_dec, then free the buffer).
// Elements are retained on store, so the per-element frees are balanced.
func TestLowerStringArrayElemReclaimOnNative(t *testing.T) {
	p := lowerSourceWith(t, `function build(a: string, b: string): i32 {
    var arr: string[] = [a, b];
    return arr[0].len();
}`, 8)
	if !callsDirect(p, "build", "__fern_drop_arr_str") {
		t.Errorf("native (ptrW=8) string[] drop must reclaim elements via __fern_drop_arr_str:\n%s", p)
	}
}

// TestLowerStringArrayElemReclaimOnArm64TwoWord — Slice 4 on arm64:
// a string[] local on the two-word ABI reclaims its element strings
// through __fern_drop_arr_str (same two-word walk as wasm). The
// per-slice IR widening gates on UseTwoWordStrings, so wasm and
// arm64-TwoWordOverride share one codegen path; native single-word
// (the !TwoWordOverride case) keeps the single-ptr drop_arr_ptr path.
func TestLowerStringArrayElemReclaimOnArm64TwoWord(t *testing.T) {
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()
	p := lowerSourceWith(t, `function build(a: string, b: string): i32 {
    var arr: string[] = [a, b];
    return arr[0].len() + arr[1].len();
}`, 8)
	if !callsDirect(p, "build", "__fern_drop_arr_str") {
		t.Errorf("arm64 two-word: expected string[] local reclamation via __fern_drop_arr_str:\n%s", p)
	}
}

// TestLowerStringArrayInStructFieldReclaimOnArm64TwoWord locks the
// nested string[] case on arm64: a `string[]` struct field reclaims
// through the same __fern_drop_arr_str routing under the two-word
// ABI, mirroring the wasm-side coverage above.
func TestLowerStringArrayInStructFieldReclaimOnArm64TwoWord(t *testing.T) {
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()
	p := lowerSourceWith(t, `struct Tags { items: string[] }
function build(a: string): i32 {
    var t: Tags = Tags { items: [a] };
    return t.items[0].len();
}`, 8)
	if !callsDirect(p, "build", "__drop_struct_Tags") {
		t.Errorf("arm64 two-word: expected the struct local to reclaim via __drop_struct_Tags:\n%s", p)
	}
	if !callsDirect(p, "__drop_struct_Tags", "__fern_drop_arr_str") {
		t.Errorf("arm64 two-word: expected __drop_struct_Tags to reclaim its string[] field via __fern_drop_arr_str:\n%s", p)
	}
}

// TestLowerStringEnumPayloadReclaim verifies a string enum payload is
// reclaimed: the owned enum local's deep-drop dec's the payload via the
// two-word __fern_str_dec at the enum's last reference. The payload is
// a fresh inline concat (moved into the box, so no construction inc —
// any alias-shaped payload would taint the enum ineligible). Non-uniform
// enum → variant-plan path (dropStructField). Gated wasm.
func TestLowerStringEnumPayloadReclaim(t *testing.T) {
	p := lowerSourceWith(t, `enum Msg { Text(string), Code(i32) }
function build(): i32 {
    var m: Msg = Text("hello" + "world");
    var got: i32 = 0;
    match (m) { Text(t) => { got = t.len(); }, Code(c) => { got = c; } }
    return got;
}`, 4)
	if !callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("expected enum string payload reclamation via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringEnumPayloadNoReclaimOnNative verifies enum string
// payload reclamation is wasm-only (ptrW=8 emits no __fern_str_dec).
func TestLowerStringEnumPayloadNoReclaimOnNative(t *testing.T) {
	p := lowerSourceWith(t, `enum Msg { Text(string), Code(i32) }
function build(): i32 {
    var m: Msg = Text("hello" + "world");
    var got: i32 = 0;
    match (m) { Text(t) => { got = t.len(); }, Code(c) => { got = c; } }
    return got;
}`, 8)
	if callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("native (ptrW=8) enum drop must not emit __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringEnumPayloadReclaimOnArm64TwoWord — Slice 5 on arm64:
// a non-uniform enum holding a string payload reclaims through the
// variant-plan path (dropStructField → __fern_str_dec) under the
// two-word ABI. Wasm and arm64-TwoWordOverride now share one
// classifier (enumVariantDropPlan kind=3 fires for both) and one
// dispatcher (dropStructField string branch fires for both). Native
// single-word (x86_64, !TwoWordOverride) keeps its rc_dec path.
func TestLowerStringEnumPayloadReclaimOnArm64TwoWord(t *testing.T) {
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()
	p := lowerSourceWith(t, `enum Msg { Text(string), Code(i32) }
function build(): i32 {
    var m: Msg = Text("hello" + "world");
    var got: i32 = 0;
    match (m) { Text(t) => { got = t.len(); }, Code(c) => { got = c; } }
    return got;
}`, 8)
	if !callsDirect(p, "build", "__fern_str_dec") {
		t.Errorf("arm64 two-word: expected enum string payload reclamation via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringNestedEnumPayloadReclaimOnArm64TwoWord locks the
// worklist-driven path: an enum field on a struct routes through the
// generated __drop_enum_<Name>, whose body must dec a string payload
// via __fern_str_dec on arm64 (the appendChildDrop gate fires under
// UseTwoWordStrings).
func TestLowerStringNestedEnumPayloadReclaimOnArm64TwoWord(t *testing.T) {
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()
	p := lowerSourceWith(t, `enum Msg { Text(string), Code(i32) }
struct Holder { m: Msg }
function build(): i32 {
    var h: Holder = Holder { m: Text("hello" + "world") };
    var got: i32 = 0;
    match (h.m) { Text(t) => { got = t.len(); }, Code(c) => { got = c; } }
    return got;
}`, 8)
	if !callsDirect(p, "__drop_enum_Msg", "__fern_str_dec") {
		t.Errorf("arm64 two-word: expected __drop_enum_Msg to reclaim its string payload via __fern_str_dec:\n%s", p)
	}
}

// closureDropCallsDirect reports whether any generated __closure_drop_*
// thunk calls `callee`.
func closureDropCallsDirect(p *Program, callee string) bool {
	for _, fn := range p.Funcs {
		if !strings.HasPrefix(fn.Name, "__closure_drop_") {
			continue
		}
		for _, op := range fn.Ops {
			if isNamedCallKind(op.Kind) && op.Str == callee {
				return true
			}
		}
	}
	return false
}

// TestLowerStringClosureCaptureReclaim verifies a string captured by a
// closure is reclaimed: the generated __closure_drop_<name> thunk dec's
// the capture via the two-word __fern_str_dec at the closure's last
// reference, and MakeClosure retains the alias-shaped capture via
// __fern_str_inc. Gated wasm.
func TestLowerStringClosureCaptureReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var s: string = "cap" + "tured";
    var f = function (): i32 { return s.len(); };
    return f();
}`, 4)
	if !closureDropCallsDirect(p, "__fern_str_dec") {
		t.Errorf("expected a __closure_drop_* thunk to reclaim the string capture via __fern_str_dec:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("expected MakeClosure to retain the string capture via __fern_str_inc:\n%s", p)
	}
}

// TestLowerStringClosureCaptureReclaimOnNative verifies the native
// (single-word x86-64) closure env drop reclaims its captured string via
// __fern_str_dec — the capture is retained at MakeEnv (__fern_rc_inc), so
// freeing the buffer at the env's rc==1 is balanced. (Phase 1e-strings
// native closure-capture slice.)
func TestLowerStringClosureCaptureReclaimOnNative(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var s: string = "cap" + "tured";
    var f = function (): i32 { return s.len(); };
    return f();
}`, 8)
	if !closureDropCallsDirect(p, "__fern_str_dec") {
		t.Errorf("native (ptrW=8) closure drop must reclaim its captured string via __fern_str_dec:\n%s", p)
	}
}

// TestLowerStringClosureCaptureReclaimOnArm64TwoWord — Slice 6 on arm64:
// a string captured by a closure under the two-word ABI is reclaimed via
// __fern_str_dec at the closure's last reference (the generated
// __closure_drop_<name> thunk), balancing the __fern_str_inc that
// MakeClosure emits to retain the alias-shaped capture. Wasm and
// arm64-TwoWordOverride share one codegen path via the
// UseTwoWordStrings gate; native single-word (x86_64, !TwoWordOverride)
// keeps its rc_dec capture-drop path.
func TestLowerStringClosureCaptureReclaimOnArm64TwoWord(t *testing.T) {
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()
	p := lowerSourceWith(t, `function build(): i32 {
    var s: string = "cap" + "tured";
    var f = function (): i32 { return s.len(); };
    return f();
}`, 8)
	if !closureDropCallsDirect(p, "__fern_str_dec") {
		t.Errorf("arm64 two-word: expected a __closure_drop_* thunk to reclaim the string capture via __fern_str_dec:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("arm64 two-word: expected MakeClosure to retain the string capture via __fern_str_inc:\n%s", p)
	}
}

// TestLowerTupleBoxReclaim verifies an owned tuple local reclaims its
// heap box at the last reference: the exit sweep must emit an
// rc==1-gated __fern_box_free. Without an rc header a tuple is never
// swept and its box leaks entirely.
func TestLowerTupleBoxReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var t: (i32, i32) = (1, 2);
    return t.0 + t.1;
}`, 8)
	if !callsDirect(p, "build", "__fern_box_free") {
		t.Errorf("expected owned tuple local to reclaim its box via __fern_box_free:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_rc_is_unique") {
		t.Errorf("expected tuple box reclamation to be rc==1 gated:\n%s", p)
	}
}

// TestLowerTupleDestructureArrayReclaim verifies the dup-on-projection +
// deep-drop path: destructuring a tuple of arrays must inc each extracted
// array (so the binding owns it), the tuple drop must deep-drop its array
// elements, and the owned bindings must free their buffers — all three
// surface as runtime calls in the lowered body.
func TestLowerTupleDestructureArrayReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var (a, b) = ([1, 2], [3, 4]);
    return a[0] + b[1];
}`, 8)
	if !callsDirect(p, "build", "__fern_rc_inc") {
		t.Errorf("expected dup-on-projection (__fern_rc_inc) for extracted array elements:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_box_free") {
		t.Errorf("expected the tuple box to be reclaimed via __fern_box_free:\n%s", p)
	}
	if !callsDirect(p, "build", "__fern_arr_dec") {
		t.Errorf("expected destructured array bindings to free their buffers via __fern_arr_dec:\n%s", p)
	}
}

// TestLowerMapStructValueReclaim verifies a Map[K, ConcreteStruct] routes
// its drop through the generated __drop_map_via_<drop> column walk, which
// deep-drops each value via __drop_struct_<V> — rather than the generic
// __map_drop_values (which only reclaims array values).
func TestLowerMapStructValueReclaim(t *testing.T) {
	p := lowerSourceWith(t, `struct Item { xs: i32[] }
function build(): i32 {
    var m: Map[i32, Item] = map_new(8);
    m = m.insert(1, Item { xs: [1, 2] });
    return 0;
}`, 8)
	const loop = "__drop_map_via___drop_struct_Item"
	if !funcExists(p, loop) {
		t.Fatalf("expected generated %s:\n%s", loop, p)
	}
	if !callsDirect(p, "build", loop) {
		t.Errorf("expected map local drop to route through %s:\n%s", loop, p)
	}
	if !callsDirect(p, loop, "__drop_struct_Item") {
		t.Errorf("expected %s to deep-drop values via __drop_struct_Item:\n%s", loop, p)
	}
}

// TestLowerMapEnumValueReclaim verifies the generalization to enum values:
// a Map[K, ConcreteEnum] deep-drops each value via __drop_enum_<V> through
// the same __drop_map_via_<drop> column walk.
func TestLowerMapEnumValueReclaim(t *testing.T) {
	p := lowerSourceWith(t, `struct VI { v: i32[] }
struct VA { v: i32[] }
type Value = VI | VA;
function build(): i32 {
    var m: Map[i32, Value] = map_new(8);
    m = m.insert(1, VI { v: [1, 2] });
    return 0;
}`, 8)
	const loop = "__drop_map_via___drop_enum_Value"
	if !funcExists(p, loop) {
		t.Fatalf("expected generated %s:\n%s", loop, p)
	}
	if !callsDirect(p, "build", loop) {
		t.Errorf("expected map local drop to route through %s:\n%s", loop, p)
	}
	if !callsDirect(p, loop, "__drop_enum_Value") {
		t.Errorf("expected %s to deep-drop values via __drop_enum_Value:\n%s", loop, p)
	}
}

// TestLowerMapInEnumDeadDropCulled pins the dead map-reclamation cull: an enum
// with a Map-typed variant, dropped WITHOUT the program ever constructing a Map
// (so core/map's __map_drop_values is never loaded), must not leave a dangling
// __map_drop_values call in the generated __drop_enum_ body. That call site is
// dead — no Map value can exist — but the static reference would otherwise fail
// as "unknown callee __map_drop_values" at wasm build / an undefined symbol on
// the register backends. Regression for the array-element / accumulator reclaim
// path (#4420) generating the enum drop fn regardless of the Map-in-enum
// "documented safe leak" gate (enumRcPayloadsEligible).
func TestLowerMapInEnumDeadDropCulled(t *testing.T) {
	p := lowerSourceWith(t, `enum E { Obj(Map[i32, i32]), Num(i32) }
function main(): i32 {
    var xs: E[] = [];
    xs = xs.append(Num(7));
    return 0;
}`, 8)
	// No map is ever constructed, so __map_drop_values must be neither loaded nor
	// referenced by any (generated) function.
	if funcExists(p, "__map_drop_values") {
		t.Fatalf("__map_drop_values should not be loaded when no map is constructed:\n%s", p)
	}
	for _, f := range p.Funcs {
		for _, op := range f.Ops {
			if op.Kind == OpCallDirect && op.Str == "__map_drop_values" {
				t.Fatalf("dead __map_drop_values call left in %q (should be culled):\n%s", f.Name, p)
			}
		}
	}
}

// TestLowerMapArrayOfStructValueReclaim verifies a Map[K, Item[]] deep-
// drops each value array's element boxes + buffer via __drop_arr_struct_<Elem>
// (through __drop_map_via_<drop>), rather than the shallow drop_arr_ptr
// that __map_drop_values uses for kind 3 (which leaks the element boxes).
func TestLowerMapArrayOfStructValueReclaim(t *testing.T) {
	p := lowerSourceWith(t, `struct Item { xs: i32[] }
function build(): i32 {
    var m: Map[i32, Item[]] = map_new(8);
    m = m.insert(1, [Item { xs: [1, 2] }]);
    return 0;
}`, 8)
	const loop = "__drop_map_via___drop_arr_struct_Item"
	if !funcExists(p, loop) {
		t.Fatalf("expected generated %s:\n%s", loop, p)
	}
	if !callsDirect(p, "build", loop) {
		t.Errorf("expected map local drop to route through %s:\n%s", loop, p)
	}
	if !callsDirect(p, loop, "__drop_arr_struct_Item") {
		t.Errorf("expected %s to deep-drop each value array via __drop_arr_struct_Item:\n%s", loop, p)
	}
	if !callsDirect(p, "__drop_arr_struct_Item", "__drop_struct_Item") {
		t.Errorf("expected __drop_arr_struct_Item to recurse into __drop_struct_Item:\n%s", p)
	}
}

// TestLowerMapGenericEnumValueReclaim verifies a heap-boxed generic-enum
// instantiation as a map value (Map[K, Option[Item]]) reclaims through the
// same __drop_map_via_<drop> walk, with the per-value drop being the
// mangled generic-enum drop (dropFnNameFor's generic path).
func TestLowerMapGenericEnumValueReclaim(t *testing.T) {
	p := lowerSourceWith(t, `struct Item { xs: i32[] }
function build(): i32 {
    var m: Map[i32, Option[Item]] = map_new(8);
    m = m.insert(1, Some(Item { xs: [1, 2] }));
    return 0;
}`, 8)
	const loop = "__drop_map_via___drop_enum_Option_LB_Item_RB_"
	if !funcExists(p, loop) {
		t.Fatalf("expected generated %s:\n%s", loop, p)
	}
	if !callsDirect(p, "build", loop) {
		t.Errorf("expected map local drop to route through %s:\n%s", loop, p)
	}
	if !callsDirect(p, loop, "__drop_enum_Option_LB_Item_RB_") {
		t.Errorf("expected %s to deep-drop values via the generic-enum drop:\n%s", loop, p)
	}
}

// TestLowerMapOverwriteDrop verifies overwriting a struct map value emits
// the pre-drop: __map_lookup_val fetches the about-to-be-replaced value
// and __drop_struct_<V> deep-drops it (the type-erased runtime
// overwrite-dec can't, so without this the replaced value leaks).
func TestLowerMapOverwriteDrop(t *testing.T) {
	p := lowerSourceWith(t, `struct Item { xs: i32[] }
function build(): i32 {
    var m: Map[i32, Item] = map_new(8);
    m = m.insert(1, Item { xs: [1, 2] });
    m = m.insert(1, Item { xs: [3] });
    return 0;
}`, 8)
	if !callsDirect(p, "build", "__map_lookup_val") {
		t.Errorf("expected overwrite pre-drop lookup via __map_lookup_val:\n%s", p)
	}
	if !callsDirect(p, "build", "__drop_struct_Item") {
		t.Errorf("expected overwrite pre-drop to deep-drop the old value via __drop_struct_Item:\n%s", p)
	}
}

// TestLowerMapStringValueReclaim verifies a Map[i32, string] (wasm)
// reclaims its string values: the map local's drop routes through the
// generated __drop_map_str_values column walk, which __fern_str_dec's
// each boxed (data, len) value buffer.
func TestLowerMapStringValueReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "hello" + "world");
    return 0;
}`, 4)
	if !funcExists(p, "__drop_map_str_values") {
		t.Fatalf("expected generated __drop_map_str_values:\n%s", p)
	}
	if !callsDirect(p, "build", "__drop_map_str_values") {
		t.Errorf("expected map local drop to route through __drop_map_str_values:\n%s", p)
	}
	if !callsDirect(p, "__drop_map_str_values", "__fern_str_dec") {
		t.Errorf("expected __drop_map_str_values to reclaim each value via __fern_str_dec:\n%s", p)
	}
}

// TestLowerMapStringValueGetRetain verifies m.get(k) on a Map[i32, string]
// retains the returned string (__fern_str_inc) so the gotten value
// co-owns the buffer alongside the map's cell.
func TestLowerMapStringValueGetRetain(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "hello" + "world");
    var got: i32 = 0;
    match (m.get(1)) { Some(v) => { got = v.len(); }, None => { got = 0; } }
    return got;
}`, 4)
	if !callsDirect(p, "build", "__fern_str_inc") {
		t.Errorf("expected m.get to retain the returned string via __fern_str_inc:\n%s", p)
	}
}

// TestLowerMapStringValueReclaimOnNative verifies native single-word
// strings (ptrW=8, !TwoWordOverride — the x86_64 path) generate
// __drop_map_str_values for Map[K, string]. The body uses the
// direct-pointer form (__fern_rc_dec on each stored data pointer; no
// cell deref, no cell_free) since x86_64 stores strings unboxed.
func TestLowerMapStringValueReclaimOnNative(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "hello" + "world");
    return 0;
}`, 8)
	if !funcExists(p, "__drop_map_str_values") {
		t.Fatalf("native single-word (ptrW=8) must generate __drop_map_str_values:\n%s", p)
	}
	if !callsDirect(p, "build", "__drop_map_str_values") {
		t.Errorf("expected map local drop to route through __drop_map_str_values:\n%s", p)
	}
	if !callsDirect(p, "__drop_map_str_values", "__fern_rc_dec") {
		t.Errorf("native __drop_map_str_values must reclaim each value via __fern_rc_dec:\n%s", p)
	}
	if callsDirect(p, "__drop_map_str_values", "__fern_cell_free") {
		t.Errorf("native __drop_map_str_values must NOT call __fern_cell_free (no cell boxing):\n%s", p)
	}
}

// TestLowerMapStringValueReclaimOnArm64TwoWord — Slice 7 on arm64:
// strings are boxed (data, len) cells under arm64-TwoWordOverride
// the same way as wasm, and #1665's runtime-helper port + the
// matching IR widening (this PR) hook the same boxed-cell column-
// walk reclamation. Map[K, string] now routes through
// __drop_map_str_values at map drop, and the per-entry body calls
// __fern_str_dec + __fern_cell_free under the two-word ABI.
// Originally an exclusion-locking gate; flipped to the inclusion-
// asserting shape once the prereqs landed.
func TestLowerMapStringValueReclaimOnArm64TwoWord(t *testing.T) {
	prev := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prev }()
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "hello" + "world");
    return 0;
}`, 8)
	if !callsDirect(p, "build", "__drop_map_str_values") {
		t.Errorf("arm64 (ptrW=8 + TwoWordOverride) must route map drop through __drop_map_str_values now that the boxed-string runtime + IR Slice 7 widening have landed:\n%s", p)
	}
}

// TestLowerMapStringKeyReclaim verifies a Map[string, V] (wasm) reclaims
// its string KEYS: the map local's drop routes through the generated
// __drop_map_str_keys column walk, which __fern_str_dec's each boxed
// (data, len) key buffer.
func TestLowerMapStringKeyReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("foo" + "bar", 10);
    return 0;
}`, 4)
	if !funcExists(p, "__drop_map_str_keys") {
		t.Fatalf("expected generated __drop_map_str_keys:\n%s", p)
	}
	if !callsDirect(p, "build", "__drop_map_str_keys") {
		t.Errorf("expected map local drop to route through __drop_map_str_keys:\n%s", p)
	}
	if !callsDirect(p, "__drop_map_str_keys", "__fern_str_dec") {
		t.Errorf("expected __drop_map_str_keys to reclaim each key via __fern_str_dec:\n%s", p)
	}
	if !callsDirect(p, "__drop_map_str_keys", "__fern_cell_free") {
		t.Errorf("expected __drop_map_str_keys to free each dead key cell via __fern_cell_free:\n%s", p)
	}
}

// TestLowerMapStringKeyReclaimOnNative is the native (ptrW=8 + single-
// word) counterpart to TestLowerMapStringKeyReclaim: x86_64 also
// generates __drop_map_str_keys, but the body uses the direct-pointer
// form (__fern_rc_dec on each stored key data pointer; no cell deref,
// no cell_free) since x86_64 stores keys unboxed.
func TestLowerMapStringKeyReclaimOnNative(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("foo" + "bar", 10);
    return 0;
}`, 8)
	if !funcExists(p, "__drop_map_str_keys") {
		t.Fatalf("native single-word (ptrW=8) must generate __drop_map_str_keys:\n%s", p)
	}
	if !callsDirect(p, "build", "__drop_map_str_keys") {
		t.Errorf("expected map local drop to route through __drop_map_str_keys:\n%s", p)
	}
	if !callsDirect(p, "__drop_map_str_keys", "__fern_rc_dec") {
		t.Errorf("native __drop_map_str_keys must reclaim each key via __fern_rc_dec:\n%s", p)
	}
	if callsDirect(p, "__drop_map_str_keys", "__fern_cell_free") {
		t.Errorf("native __drop_map_str_keys must NOT call __fern_cell_free (no cell boxing):\n%s", p)
	}
}

// TestLowerMapStringKeyReclaimOnArm64TwoWord — Slice 8 on arm64:
// the matching key-side flip of Slice 7. Strings as Map KEYS are
// boxed (data, len) cells under arm64-TwoWordOverride the same way
// as on wasm, and the __drop_map_str_keys column-walk reclamation
// works on either backend now (genMapStrColDropFn branches on
// UseTwoWordStrings to pick the boxed-cell vs direct-pointer body
// shape internally). Originally an exclusion-locking gate; flipped
// to inclusion-asserting once #1665's runtime helpers + the Slice 7
// IR widening landed.
func TestLowerMapStringKeyReclaimOnArm64TwoWord(t *testing.T) {
	prev := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prev }()
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("foo" + "bar", 10);
    return 0;
}`, 8)
	if !callsDirect(p, "build", "__drop_map_str_keys") {
		t.Errorf("arm64 (ptrW=8 + TwoWordOverride) must route map drop through __drop_map_str_keys now that the boxed-string runtime + IR Slice 8 widening have landed:\n%s", p)
	}
}

// TestLowerMapStringColDropFreesCell verifies the string-column drop walk
// frees the boxed K/V cell itself (__fern_cell_free) in addition to
// reclaiming the buffer (__fern_str_dec) — the cell is a raw 16-byte
// freelist-class alloc that leaked before this slice.
func TestLowerMapStringColDropFreesCell(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "hello" + "world");
    return 0;
}`, 4)
	if !callsDirect(p, "__drop_map_str_values", "__fern_cell_free") {
		t.Errorf("expected __drop_map_str_values to free each dead value cell via __fern_cell_free:\n%s", p)
	}
}

// TestLowerMapStringLookupKeyCellFreed verifies that the read-only Map
// methods (get / has / get_or / without) reclaim the transient boxed
// lookup-key cell via __fern_cell_free — the read helpers never retain
// the key, so the per-call cell is freed once the helper has consumed it.
func TestLowerMapStringLookupKeyCellFreed(t *testing.T) {
	cases := []struct{ name, call string }{
		{"get", `match (m.get("a" + "b")) { Some(v) => { return v; }, None => { return 0; } }`},
		{"has", `if (m.has("a" + "b")) { return 1; } return 0;`},
		{"get_or", `return m.get_or("a" + "b", 0);`},
		{"delete", `var r = m.without("a" + "b"); return 0;`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a" + "b", 1);
    `+c.call+`
}`, 4)
			if !callsDirect(p, "build", "__fern_cell_free") {
				t.Errorf("expected %s lookup to free the transient key cell via __fern_cell_free:\n%s", c.name, p)
			}
		})
	}
}

// TestLowerMapStringLookupKeyNoFreeOnNative verifies the transient
// lookup-key cell reclamation is wasm-only (ptrW=8 keeps strings
// single-pointer and never boxes lookup keys into freeable cells).
func TestLowerMapStringLookupKeyNoFreeOnNative(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a" + "b", 1);
    return m.get_or("a" + "b", 0);
}`, 8)
	if callsDirect(p, "build", "__fern_cell_free") {
		t.Errorf("native (ptrW=8) must not free lookup-key cells:\n%s", p)
	}
}

// TestLowerMapStringKeyAndValueReclaim verifies a Map[string, string]
// (wasm) reclaims BOTH columns: independent key + value walks both run at
// the map's drop.
func TestLowerMapStringKeyAndValueReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[string, string] = map_new(8);
    m = m.insert("foo" + "bar", "baz" + "qux");
    return 0;
}`, 4)
	if !callsDirect(p, "build", "__drop_map_str_keys") {
		t.Errorf("expected key-column drop via __drop_map_str_keys:\n%s", p)
	}
	if !callsDirect(p, "build", "__drop_map_str_values") {
		t.Errorf("expected value-column drop via __drop_map_str_values:\n%s", p)
	}
}

// Superseded by TestLowerMapStringKeyReclaimOnNative (single-word does
// reclaim) and TestLowerMapStringKeyNoReclaimOnArm64TwoWord (arm64-
// two-word is still excluded). See those two tests above.

// anyTupleDropFn reports whether the program contains at least one
// __drop_tuple_<mangled> function — used by the nested-tuple-reclaim
// tests below, which don't care about the exact mangled name (it
// changes when the tuple shape changes).
func anyTupleDropFn(p *Program) (*Func, bool) {
	for _, fn := range p.Funcs {
		if strings.HasPrefix(fn.Name, "__drop_tuple_") {
			return fn, true
		}
	}
	return nil, false
}

// callsAnyTupleDrop reports whether any function in p calls any
// __drop_tuple_<mangled> helper.
func callsAnyTupleDrop(p *Program) bool {
	for _, fn := range p.Funcs {
		for _, op := range fn.Ops {
			if op.Kind == OpCallDirect && strings.HasPrefix(op.Str, "__drop_tuple_") {
				return true
			}
		}
	}
	return false
}

// TestLowerNestedTupleStringInStructReclaim — a struct field that holds
// a tuple containing a string used to leak the string buffer: the
// struct drop (inline at the local's scope exit OR via a generated
// __drop_struct_<Name> when the struct is itself a nested field) flat-
// dec'd the tuple ptr via __fern_rc_dec (freeing the tuple box on its
// rc==0) but never traversed the tuple's elements, so the string
// buffer survived past its last reachable reference. With the nested-
// tuple-drop fix, dropFnNameFor routes the tuple field through a
// generated __drop_tuple_<mangled> helper that is_unique-gates the
// tuple ptr, reclaims its string element via __fern_str_dec (wasm
// two-word), then returns the tuple box to the freelist.
func TestLowerNestedTupleStringInStructReclaim(t *testing.T) {
	p := lowerSourceWith(t, `struct Box { items: (string, i32) }
function build(): i32 {
    var b: Box = Box { items: ("h" + "i", 7) };
    return b.items.1;
}`, 4)
	if !callsAnyTupleDrop(p) {
		t.Errorf("expected the Box's struct drop to route its tuple field through a generated __drop_tuple_<...> helper; got:\n%s", p)
	}
	td, ok := anyTupleDropFn(p)
	if !ok {
		t.Fatalf("expected a generated __drop_tuple_<...> helper to materialise for the struct's tuple field; got:\n%s", p)
	}
	sawStrDec := false
	for _, op := range td.Ops {
		if op.Kind == OpCallDirect && op.Str == "__fern_str_dec" {
			sawStrDec = true
		}
	}
	if !sawStrDec {
		t.Errorf("expected the generated %s body to dec its string element via __fern_str_dec on wasm; got:\n%s", td.Name, p)
	}
}

// TestLowerNestedTupleStringInStructReclaimOnNativeSingleWord — same
// as the wasm test above but for the native single-word string ABI:
// the generated __drop_tuple_<...> helper must dec the string element
// via __fern_rc_dec (not __fern_str_dec, which is wasm-only). The
// matching alias inc at construction time goes through __fern_rc_inc
// via emitAliasInc's existing native fall-through.
func TestLowerNestedTupleStringInStructReclaimOnNativeSingleWord(t *testing.T) {
	p := lowerSourceWith(t, `struct Box { items: (string, i32) }
function build(): i32 {
    var b: Box = Box { items: ("h" + "i", 7) };
    return b.items.1;
}`, 8)
	td, ok := anyTupleDropFn(p)
	if !ok {
		t.Fatalf("expected a generated __drop_tuple_<...> helper on native single-word; got:\n%s", p)
	}
	sawRcDec := false
	for _, op := range td.Ops {
		if op.Kind == OpRcDec {
			sawRcDec = true
		}
	}
	if !sawRcDec {
		t.Errorf("expected the generated %s body to dec its string element via __fern_rc_dec on native single-word; got:\n%s", td.Name, p)
	}
}

// TestLowerNestedTupleStringReclaimOnArm64TwoWord — Slice 3 follow-up
// on arm64: a nested tuple `(string, i32)` held in a struct field
// reclaims its string element through a generated __drop_tuple_<...>
// helper whose body dec's via __fern_str_dec under the two-word ABI.
// Originally an exclusion (the helper symbol existed only on wasm);
// post-#1665 the arm64 string-runtime port (and the four prior arm64
// slice widenings) made the two-word string element a safe one-line
// widening — wasm and arm64-TwoWordOverride now share one codegen path.
func TestLowerNestedTupleStringReclaimOnArm64TwoWord(t *testing.T) {
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()
	p := lowerSourceWith(t, `struct Box { items: (string, i32) }
function build(): i32 {
    var b: Box = Box { items: ("h" + "i", 7) };
    return b.items.1;
}`, 8)
	td, ok := anyTupleDropFn(p)
	if !ok {
		t.Fatalf("arm64 two-word: expected a generated __drop_tuple_<...> helper for the struct's tuple field; got:\n%s", p)
	}
	sawStrDec := false
	for _, op := range td.Ops {
		if op.Kind == OpCallDirect && op.Str == "__fern_str_dec" {
			sawStrDec = true
		}
	}
	if !sawStrDec {
		t.Errorf("arm64 two-word: expected %s to dec its string element via __fern_str_dec; got:\n%s", td.Name, p)
	}
}

// TestLowerEnumPayloadTupleStringReclaim pins coverage of the second
// nested-tuple shape the __drop_tuple_<mangled> routing closes: an
// ENUM PAYLOAD that's a tuple holding a string. The variant's payload
// drop — both the generated __drop_enum_<Name> (the worklist-driven
// nested-field path) and the inline tag-dispatch path
// (enumVariantDropPlan in emitDec) — routes the TupleType payload
// through __drop_tuple_<...>, and that helper dec's the string
// element. The variant-plan path only fires for the eligible / freel-
// igible flow, so the test uses a direct construction inside an
// outer struct (which itself drops through __drop_struct_<Outer> →
// __drop_enum_<Wrap> → __drop_tuple_<...> end-to-end).
func TestLowerEnumPayloadTupleStringReclaim(t *testing.T) {
	p := lowerSourceWith(t, `enum Wrap { Pair((string, i32)), Empty }
struct Holder { w: Wrap }
function build(): i32 {
    var h: Holder = Holder { w: Pair(("h" + "i", 7)) };
    var r: i32 = 0;
    match (h.w) { Pair(q) => { r = q.1; }, Empty => { r = 0; } }
    return r;
}`, 4)
	td, ok := anyTupleDropFn(p)
	if !ok {
		t.Fatalf("expected a generated __drop_tuple_<...> helper to materialise for the enum's tuple payload; got:\n%s", p)
	}
	sawStrDec := false
	for _, op := range td.Ops {
		if op.Kind == OpCallDirect && op.Str == "__fern_str_dec" {
			sawStrDec = true
		}
	}
	if !sawStrDec {
		t.Errorf("expected the generated %s body to dec its string element via __fern_str_dec on wasm; got:\n%s", td.Name, p)
	}
}

// TestLowerClosureCaptureTupleStringReclaim pins coverage of the
// third nested-tuple shape: a CLOSURE CAPTURE that's a tuple holding
// a string. genClosureDropThunk routes a capture through
// dropFnNameFor (which already did for arrays / structs / enums) —
// post-fix it returns __drop_tuple_<...> for a tuple capture too. The
// thunk then calls into that helper at the closure's last reference.
func TestLowerClosureCaptureTupleStringReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var p: (string, i32) = ("h" + "i", 7);
    var f: () => i32 = function(): i32 { return p.1; };
    return f();
}`, 4)
	td, ok := anyTupleDropFn(p)
	if !ok {
		t.Fatalf("expected a generated __drop_tuple_<...> helper to materialise for the closure's tuple capture; got:\n%s", p)
	}
	sawStrDec := false
	for _, op := range td.Ops {
		if op.Kind == OpCallDirect && op.Str == "__fern_str_dec" {
			sawStrDec = true
		}
	}
	if !sawStrDec {
		t.Errorf("expected the generated %s body to dec its string element via __fern_str_dec on wasm; got:\n%s", td.Name, p)
	}
	if !closureDropCallsDirect(p, td.Name) {
		t.Errorf("expected the generated closure drop thunk to invoke %s on the tuple capture; got:\n%s", td.Name, p)
	}
}

// TestLowerArrayOfTupleStringReclaim pins the array-of-tuple sibling
// of the nested-tuple-drop fix. `(string, i32)[]` used to leak its
// elements' strings: arrElemStructDropName only recognised concrete
// struct elements, so the local-side ARRAY drop fell through to the
// flat __fern_drop_arr_ptr which only rc_dec's each element pointer
// (freeing the tuple boxes but never traversing them). Post-fix the
// routing recognises tuple elements and emits a per-element
// __drop_tuple_<mangled> loop that dec's each tuple's string element
// before freeing the buffer.
//
// The assertion is loose on call-site placement (the per-element loop
// may live in the local-side ARRAY drop OR a generated
// __drop_arr_tuple_<mangled> helper) — both shapes satisfy the
// invariant we care about: SOME function calls __drop_tuple_<...>
// while the array is being reclaimed.
func TestLowerArrayOfTupleStringReclaim(t *testing.T) {
	p := lowerSourceWith(t, `function build(): i32 {
    var a: (string, i32)[] = [("h" + "i", 7)];
    return a[0].1;
}`, 4)
	td, ok := anyTupleDropFn(p)
	if !ok {
		t.Fatalf("expected a generated __drop_tuple_<...> helper to materialise for the array's tuple element; got:\n%s", p)
	}
	sawStrDec := false
	for _, op := range td.Ops {
		if op.Kind == OpCallDirect && op.Str == "__fern_str_dec" {
			sawStrDec = true
		}
	}
	if !sawStrDec {
		t.Errorf("expected the generated %s body to dec its string element via __fern_str_dec on wasm; got:\n%s", td.Name, p)
	}
	// Pin that SOMEONE actually invokes the helper — without a call
	// site the leak fix is paper-only.
	called := false
	for _, fn := range p.Funcs {
		for _, op := range fn.Ops {
			if op.Kind == OpCallDirect && op.Str == td.Name {
				called = true
				break
			}
		}
		if called {
			break
		}
	}
	if !called {
		t.Errorf("expected SOME function to call %s while reclaiming the array's tuple elements; got:\n%s", td.Name, p)
	}
}
