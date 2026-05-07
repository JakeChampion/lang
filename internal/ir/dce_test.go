package ir

import "testing"

// loweredAndDCE parses, type-checks, lowers, and runs the IR's DCE
// pass on src. Most tests inspect the resulting op count / specific
// op kinds.
func loweredAndDCE(t *testing.T, src string) *Program {
	t.Helper()
	p := lowerSource(t, src)
	EliminateDeadCode(p)
	return p
}

// The lowering pass tacks an implicit return onto every function so
// the body always ends with OpReturn / OpReturnVoid. When the source
// itself ends with `return`, that synthetic tail is unreachable —
// DCE should drop it.
func TestDCEStripsImplicitReturnAfterExplicitOne(t *testing.T) {
	p := loweredAndDCE(t, `function f(): number { return 7; }`)
	fn := findFunc(p, "f")
	if fn == nil {
		t.Fatal("f not found")
	}
	// Expect exactly: OpConstI32 7, OpReturn.
	if len(fn.Ops) != 2 {
		t.Fatalf("got %d ops, want 2 (const + return):\n%s", len(fn.Ops), p)
	}
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[1].Kind != OpReturn {
		t.Errorf("ops = [%s, %s], want [OpConstI32, OpReturn]",
			fn.Ops[0].Kind, fn.Ops[1].Kind)
	}
}

// Statements after a `return` in the same block are unreachable.
// (The AST-level optimiser already drops these, so this test relies
// on hand-constructed IR to bypass it. Once the AST optimiser's DCE
// retires, this'll be the only DCE on the path.)
func TestDCEDropsOpsAfterTerminator(t *testing.T) {
	// Hand-built op slice: const 1, return, const 2, return.
	// The trailing const + return are unreachable.
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 1},
			{Kind: OpReturn},
			{Kind: OpConstI32, I32: 2},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	EliminateDeadCode(p)
	if len(fn.Ops) != 2 {
		t.Fatalf("got %d ops, want 2:\n%s", len(fn.Ops), p)
	}
}

// In an if-statement, a return in the then-arm makes ops between
// that return and the OpElse (or OpEnd if no else) dead — but the
// else-arm starts fresh. Hand-build the IR so we don't depend on
// other passes.
func TestDCETerminatorInThenArmDoesNotKillElse(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 1}, // cond
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpConstI32, I32: 10}, // dead-code marker
			{Kind: OpReturn},
			{Kind: OpConstI32, I32: 99}, // dead inside then-arm
			{Kind: OpDrop},              // dead inside then-arm
			{Kind: OpElse},
			{Kind: OpConstI32, I32: 20}, // alive in else-arm
			{Kind: OpReturn},
			{Kind: OpEnd},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	EliminateDeadCode(p)
	// Walk the result and verify the else arm survived but the
	// dead 99 / drop pair did not.
	saw99, sawElse, saw20 := false, false, false
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 99 {
			saw99 = true
		}
		if op.Kind == OpElse {
			sawElse = true
		}
		if op.Kind == OpConstI32 && op.I32 == 20 {
			saw20 = true
		}
	}
	if saw99 {
		t.Errorf("dead op (const 99) should have been dropped:\n%s", p)
	}
	if !sawElse {
		t.Errorf("OpElse must survive — it terminates the dead region:\n%s", p)
	}
	if !saw20 {
		t.Errorf("else-arm op (const 20) must survive:\n%s", p)
	}
}

// Ops after an unconditional OpBr (within the same scope) are dead.
// The OpBr itself stays.
func TestDCEDropsOpsAfterUnconditionalBranch(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpBlock, I32: BlockTypeVoid},
			{Kind: OpBr, I32: 0},
			{Kind: OpConstI32, I32: 99}, // dead
			{Kind: OpEnd},
			{Kind: OpConstI32, I32: 1}, // alive
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	EliminateDeadCode(p)
	saw99 := false
	saw1 := false
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 99 {
			saw99 = true
		}
		if op.Kind == OpConstI32 && op.I32 == 1 {
			saw1 = true
		}
	}
	if saw99 {
		t.Errorf("op after `br 0` should have been dropped:\n%s", p)
	}
	if !saw1 {
		t.Errorf("op past the OpEnd of the dead region must survive:\n%s", p)
	}
}

// `OpBrIf` is conditional — it doesn't make subsequent ops dead.
// Code following `br_if` falls through when the branch isn't taken.
func TestDCELeavesOpsAfterConditionalBranch(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpBlock, I32: BlockTypeVoid},
			{Kind: OpConstI32, I32: 0}, // cond
			{Kind: OpBrIf, I32: 0},
			{Kind: OpConstI32, I32: 1}, // alive — fallthrough path
			{Kind: OpDrop},
			{Kind: OpEnd},
			{Kind: OpReturnVoid},
		},
	}
	before := len(fn.Ops)
	p := &Program{Funcs: []*Func{fn}}
	EliminateDeadCode(p)
	if len(fn.Ops) != before {
		t.Errorf("br_if must not trigger DCE: %d ops → %d:\n%s", before, len(fn.Ops), p)
	}
}

// Dead nested scopes still need their OpEnds counted so the depth
// tracker doesn't lose its place. Verify a return-followed-by-block
// drops both correctly without leaving an unbalanced End behind.
func TestDCEHandlesDeadNestedScope(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 1},
			{Kind: OpReturn},
			{Kind: OpBlock, I32: BlockTypeVoid}, // dead
			{Kind: OpConstI32, I32: 99},         // dead
			{Kind: OpEnd},                       // closes the dead block
			// Function-end terminator. Without it the surrounding
			// scope tracker would never re-rise above deadDepth=0.
			{Kind: OpReturnVoid},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	EliminateDeadCode(p)
	// Nothing past the first `return` survives — both the inner
	// block scope and the trailing return-void.
	if len(fn.Ops) != 2 {
		t.Errorf("expected 2 surviving ops, got %d:\n%s", len(fn.Ops), p)
	}
}

// DCE is idempotent: a second pass reproduces the same op slice.
func TestDCEIsIdempotent(t *testing.T) {
	p := loweredAndDCE(t, `function f(): number {
		if (true) { return 1; }
		return 2;
	}`)
	before := p.String()
	EliminateDeadCode(p)
	after := p.String()
	if before != after {
		t.Errorf("DCE not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// DCE must preserve structural-CF invariants — every Block / Loop /
// If still matched by an End, depth never negative, depth back to
// 0 at function end.
func TestDCEKeepsStructuredCFBalanced(t *testing.T) {
	p := loweredAndDCE(t, `function f(n: number): number {
		if (n == 0) { return 99; }
		if (n > 100) {
			while (n > 50) { n = n - 1; }
		}
		return n;
	}`)
	for _, fn := range p.Funcs {
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
			}
		}
		if depth != 0 {
			t.Errorf("%s: ended at depth %d, want 0", fn.Name, depth)
		}
	}
}
