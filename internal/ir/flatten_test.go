package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// `if (c) { return X; } return Y;` flattens to a single typed if
// followed by one return. The original two OpReturns collapse to
// one — both arms now leave the value on the operand stack and the
// trailing return consumes it once.
func TestFlattenIfReturnAndTrailingReturn(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): i32 {
		if (n == 0) { return 1; }
		return 2;
	}`)
	FlattenBranches(p)
	fn := findFunc(p, "f")
	// Expect one OpReturn total — the consolidated trailing one.
	count := 0
	for _, op := range fn.Ops {
		if op.Kind == OpReturn {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one OpReturn after flatten, got %d:\n%s", count, p)
	}
	// And an OpElse must show up — the rewrite turns the no-else
	// if into a typed if/else.
	mustContainOp(t, p, "f", OpElse)
}

// The rewritten if carries the function's return type so the
// validator knows what each arm pushes. Float-returning function
// gets `if (result f32)`.
func TestFlattenPreservesReturnType(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): f32 {
		if (n == 0) { return 1.5; }
		return 2.5;
	}`)
	FlattenBranches(p)
	fn := findFunc(p, "f")
	hasFloatIf := false
	for _, op := range fn.Ops {
		if op.Kind == OpIf && op.I32 == BlockTypeF32 {
			hasFloatIf = true
		}
	}
	if !hasFloatIf {
		t.Errorf("expected `if (result f32)` after flatten:\n%s", p)
	}
}

// An if that already has an explicit else doesn't need flattening
// — the rewrite skips it cleanly.
func TestFlattenLeavesIfWithElseAlone(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): i32 {
		if (n == 0) { return 1; } else { return 2; }
	}`)
	before := p.String()
	FlattenBranches(p)
	after := p.String()
	if before != after {
		t.Errorf("flatten should leave if-with-else untouched:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The continuation between the if-end and the trailing return must
// be straight-line; control flow inside disqualifies the rewrite.
// (A while loop in the continuation makes the depth math complex
// and the splice unsound — leave it for a smarter analysis.)
func TestFlattenSkipsContinuationWithControlFlow(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): i32 {
		if (n == 0) { return 1; }
		var sum: i32 = 0;
		while (n > 0) { sum = sum + n; n = n - 1; }
		return sum;
	}`)
	before := p.String()
	FlattenBranches(p)
	after := p.String()
	if before != after {
		t.Errorf("flatten should bail when continuation has a loop:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Once flattened, ir.Fold can prune the conditional when the
// condition is a known constant. This composes the value of
// flatten + fold: `if (true) { return 1; } return 2;` collapses
// to `return 1`.
func TestFlattenComposesWithConstIfPruning(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		if (true) { return 1; }
		return 2;
	}`)
	FlattenBranches(p)
	Fold(p)
	fn := findFunc(p, "f")
	// After both passes, only the surviving branch's value (const 1)
	// plus a single return should remain.
	for _, op := range fn.Ops {
		if op.Kind == OpIf || op.Kind == OpElse || op.Kind == OpEnd {
			t.Fatalf("constant-if should have been pruned post-flatten:\n%s", p)
		}
	}
	hasOne := false
	hasTwo := false
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 {
			if op.I32 == 1 {
				hasOne = true
			}
			if op.I32 == 2 {
				hasTwo = true
			}
		}
	}
	if !hasOne {
		t.Errorf("surviving const 1 missing:\n%s", p)
	}
	if hasTwo {
		t.Errorf("dead-branch const 2 should be gone:\n%s", p)
	}
}

// Void-returning functions flatten too: both `OpReturnVoid`s merge
// into a single trailing OpReturnVoid after a void-typed if.
func TestFlattenVoidFunction(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): void {
		if (n == 0) { return; }
		return;
	}`)
	FlattenBranches(p)
	fn := findFunc(p, "f")
	count := 0
	for _, op := range fn.Ops {
		if op.Kind == OpReturnVoid {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected one OpReturnVoid after flatten, got %d:\n%s", count, p)
	}
	hasVoidIf := false
	for _, op := range fn.Ops {
		if op.Kind == OpIf && op.I32 == BlockTypeVoid {
			hasVoidIf = true
		}
	}
	if !hasVoidIf {
		t.Errorf("expected `if void` (matches void return) after flatten:\n%s", p)
	}
}

// Flatten preserves structured-CF balance: every Block / Loop / If
// is matched by an End, depth never goes negative, br depths in
// range.
func TestFlattenKeepsStructuredCFBalanced(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): i32 {
		if (n == 0) { return 99; }
		return n + 1;
	}`)
	FlattenBranches(p)
	fn := findFunc(p, "f")
	depth := 0
	for i, op := range fn.Ops {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			depth--
			if depth < 0 {
				t.Fatalf("op %d (%s): depth went negative", i, op.Kind)
			}
		case OpBr, OpBrIf:
			if op.I32 < 0 || op.I32 > int32(depth-1) {
				t.Errorf("op %d (%s %d): branch depth out of range (depth=%d)",
					i, op.Kind, op.I32, depth)
			}
		}
	}
	if depth != 0 {
		t.Errorf("ended at depth %d, want 0", depth)
	}
}

// Flatten must NOT fuse an if-then-return into an if-result when
// the surrounding operand stack carries values pushed BEFORE the
// if was opened. The classic failure shape is `?` propagation
// embedded in an expression context: `(EXPR * (Some(x))?)` lowers
// to `push EXPR; ...; if void { None; return; } load payload`
// where the i32.mul that follows reads `EXPR` from below the
// if-block's stack frame. Rewriting that as `if (result i32) {
// None } else { load_payload; i32.mul; ... } return` is
// unsound: wasm's `if (result T)` block can't reach the EXPR
// value pushed before the if. The continuation Y's i32.mul
// would then dip the simulated data depth below 0 — the
// stack-effect guard skips the flatten and the original if-void
// + payload-load shape stays.
//
// Caught in the wild by fernsmith seed 3089; the offending
// program nested `?` two deep inside `Some(...)`'s payload, and
// the wasm validator surfaced it as "expected i32 but nothing
// on stack at offset 547".
func TestFlattenSkipsExpressionContextTryOp(t *testing.T) {
	p := lowerSource(t, `function f(): Option[i32] {
		return Some(2 * ((Some(7))?));
	}`)
	before := p.String()
	FlattenBranches(p)
	after := p.String()
	if before != after {
		t.Errorf("flatten should leave expression-context TryOp untouched:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Idempotent: a second pass produces the same op list.
func TestFlattenIsIdempotent(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): i32 {
		if (n == 0) { return 1; }
		return 2;
	}`)
	FlattenBranches(p)
	before := p.String()
	FlattenBranches(p)
	after := p.String()
	if before != after {
		t.Errorf("FlattenBranches not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The block type the rewrite stamps on the `if` has to match how many
// operand slots each arm actually leaves. A string-returning function
// leaves two under the two-word ABI, which `ast.TwoWordOverride` turns
// on for arm64 (ptrW 8) as well as wasm32 — so the block type cannot be
// chosen by pointer width. Verified through the stack checker, which is
// what a wrong count would break (and what `ir.Inline` reuses for the
// wrapper block it puts around an inlined callee).
func TestFlattenStringReturnBlockTypeFollowsTheStringABI(t *testing.T) {
	const src = `function pick(c: boolean): string {
		if (c) { return "yes"; }
		return "no";
	}
	function main(): i32 { print(pick(true)); return 0; }`

	prev := ast.TwoWordOverride
	defer func() { ast.TwoWordOverride = prev }()

	for _, tc := range []struct {
		name     string
		ptrW     int
		override bool
		want     int32
	}{
		{"wasm32", 4, false, BlockTypeStringPair},
		{"arm64_two_word", 8, true, BlockTypeStringPair},
		{"native_one_word", 8, false, BlockTypeI32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ast.TwoWordOverride = tc.override
			p := lowerSourceWith(t, src, tc.ptrW)
			FlattenBranches(p)
			fn := findFunc(p, "pick")
			got := int32(-1)
			for _, op := range fn.Ops {
				if op.Kind == OpIf {
					got = op.I32
					break
				}
			}
			if got != tc.want {
				t.Errorf("flattened `if` block type = %d, want %d:\n%s", got, tc.want, p)
			}
			for _, prob := range mustVerifyStack(t, p) {
				t.Errorf("%s op %d %v: %s", prob.Func, prob.Op, prob.Kind, prob.Msg)
			}
		})
	}
}

// mustVerifyStack returns the stack-discipline problems Verify found in
// the functions it could model, failing the test if it modelled none.
func mustVerifyStack(t *testing.T, p *Program) []Problem {
	t.Helper()
	probs, cov := Verify(p)
	if cov.Modelled == 0 {
		t.Fatalf("the stack checker modelled no function — the assertion below would be vacuous (skips: %v)", cov.Skipped)
	}
	return probs
}
