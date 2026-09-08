package semir

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func checkedUnitPlans(t *testing.T, source string) (*Program, map[*Func]*functionUnits) {
	t.Helper()
	prog, info := checkedProgram(t, source)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := planProgramUnits(p)
	if err != nil {
		t.Fatal(err)
	}
	return p, plans
}

func verifiedUnitPlan(t *testing.T, f *Func) *functionUnits {
	t.Helper()
	p, err := planFunctionUnits(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyFunctionUnits(p, map[*Func]*functionUnits{f: p}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckedUnitBalance(t *testing.T) {
	for _, source := range []string{
		`function pilot(items: string[]): string[] { return items; }`,
		`function pilot(own items: string[]): string[] { return items; }`,
		`function pilot(own items: string[]): string { return items[0]; }`,
		`function pilot(own items: string[]): i32 { return 3; }`,
		`function pilot(item: string): string { var a = [item]; return a[0]; }`,
		`function pilot(items: string[][]): string { return items[0][0]; }`,
		`function pilot(items: string[]): string[] { return items.append(items[0]); }`,
		`function pilot(own items: string[], choose: boolean): string { var item = items[0]; if (choose) { return item; } return "other"; }`,
		`function pilot(items: string[]): string { return get(items); } function get(items: string[]): string { return items[0]; }`,
		`function pilot(own items: string[]): string { return get(items); } function get(own items: string[]): string { return items[0]; }`,
		`function pilot(item: string): string { var a = wrap(item); return a[0]; } function wrap(item: string): string[] { return [item]; }`,
		`function pilot(items: string[], stop: boolean): string { if (stop) { return items[0]; } return next(items, stop); } function next(items: string[], stop: boolean): string { return pilot(items, stop); }`,
		`function pilot(items: string[]): string { return pilot(items); }`,
		`function pilot(item: string): (string, string) { return (item, item); }`,
	} {
		t.Run(source, func(t *testing.T) { checkedUnitPlans(t, source) })
	}
}

func TestBorrowedProjectionKeepsOwnerThroughLaterBlock(t *testing.T) {
	f := newFunc("late", ast.StringType{})
	item := f.addParam(ast.StringType{}, ParamBorrow, ast.Position{})
	entry, mid, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	array := f.addOp(entry, ssa.OpArrayMake, ast.ArrayType{Elem: ast.StringType{}}, ast.Position{}, item)
	index := f.addOp(entry, ssa.OpConstInt, ast.NumberType{}, ast.Position{})
	child := f.addOp(entry, ssa.OpArrayGet, ast.StringType{}, ast.Position{}, array, index)
	f.graph.SetBr(entry, mid)
	f.graph.SetBr(mid, exit)
	f.graph.SetRet(exit, child)
	p := verifiedUnitPlan(t, f)
	ordinary := ssa.ComputeLiveness(f.graph)
	if ordinary.LiveOut[entry][array.ID] || !p.lifetime.live.LiveOut[entry][array.ID] || !p.lifetime.live.LiveIn[exit][array.ID] {
		t.Fatal("borrow lifetime was confused with ordinary register liveness")
	}
	ret := p.returns[exit]
	if len(ret.supplies) != 1 || ret.supplies[0].value != child || ret.supplies[0].mode != unitRetain || !slices.Equal(ret.drops, []ssa.Value{array}) {
		t.Fatalf("return must acquire child before dropping parent: %+v", ret)
	}
	// Original def-use edges are unchanged; added lifetime facts are explicit
	// analysis input, not a mutation that low-level code motion could ignore.
	if len(entry.Ops[2].Args) != 2 {
		t.Fatal("lifetime analysis mutated semantic operands")
	}
}

func branchProjectionUnits(t *testing.T) (*functionUnits, *ssa.Block, *ssa.Block, *ssa.Block, ssa.Value, ssa.Value) {
	t.Helper()
	f := newFunc("branch_child", ast.StringType{})
	cond := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	item := f.addParam(ast.StringType{}, ParamBorrow, ast.Position{})
	entry, yes, no, join := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(entry, cond, yes, no)
	makeChild := func(b *ssa.Block) (ssa.Value, ssa.Value) {
		array := f.addOp(b, ssa.OpArrayMake, ast.ArrayType{Elem: ast.StringType{}}, ast.Position{}, item)
		index := f.addOp(b, ssa.OpConstInt, ast.NumberType{}, ast.Position{})
		child := f.addOp(b, ssa.OpArrayGet, ast.StringType{}, ast.Position{}, array, index)
		return array, child
	}
	leftParent, left := makeChild(yes)
	rightParent, right := makeChild(no)
	f.graph.SetBr(yes, join)
	f.graph.SetBr(no, join)
	result := f.addPhi(join, ast.StringType{}, ast.Position{}, left, right)
	f.graph.SetRet(join, result)
	return verifiedUnitPlan(t, f), yes, no, join, leftParent, rightParent
}

func TestBranchLocalBorrowAnchorsStayOnIncomingEdges(t *testing.T) {
	p, yes, no, join, left, right := branchProjectionUnits(t)
	for _, tc := range []struct {
		from   *ssa.Block
		parent ssa.Value
	}{{yes, left}, {no, right}} {
		step := p.edges[flowEdge{tc.from, join}]
		if len(step.supplies) != 1 || step.supplies[0].mode != unitRetain || !slices.Equal(step.drops, []ssa.Value{tc.parent}) {
			t.Fatalf("incoming edge must secure child before parent release: %+v", step)
		}
		if p.lifetime.live.LiveIn[join][tc.parent.ID] {
			t.Fatal("non-dominating branch parent leaked into join invariant")
		}
	}
	if p.returns[join].supplies[0].mode != unitMove {
		t.Fatal("phi's incoming unit should transfer out without another retain")
	}
}

func TestRepeatedStoresTransferOnlyOneUnit(t *testing.T) {
	f := newFunc("twice", ast.ArrayType{Elem: ast.StringType{}})
	item := f.addParam(ast.StringType{}, ParamCounted, ast.Position{})
	b := f.graph.NewBlock()
	array := f.addOp(b, ssa.OpArrayMake, f.result, ast.Position{}, item, item)
	f.graph.SetRet(b, array)
	p := verifiedUnitPlan(t, f)
	step := p.ops[b.Ops[0]]
	if len(step.supplies) != 2 || step.supplies[0].mode != unitRetain || step.supplies[1].mode != unitMove || len(step.drops) != 0 {
		t.Fatalf("one retain and one transfer required: %+v", step)
	}
	step.supplies[0].mode = unitMove
	p.ops[b.Ops[0]] = step
	if err := verifyFunctionUnits(p, nil); err == nil || !strings.Contains(err.Error(), "transferred twice") {
		t.Fatalf("double transfer accepted: %v", err)
	}
}

func TestBorrowedCallArgumentPreventsAnchoringUnitTransfer(t *testing.T) {
	p, plans := checkedUnitPlans(t, `
function pilot(own items: string[]): string { return take(items[0], items); }
function take(item: string, own items: string[]): string { return item; }
`)
	plan := plans[p.funcs[0]]
	for op, step := range plan.ops {
		if op.Kind != ssa.OpSemanticCall {
			continue
		}
		if len(step.supplies) != 1 || step.supplies[0].mode != unitRetain || !slices.Equal(step.drops, []ssa.Value{op.Args[1]}) {
			t.Fatalf("borrowed child needs caller's array unit across call: %+v", step)
		}
		step.supplies[0].mode = unitMove
		step.drops = nil
		plan.ops[op] = step
		if err := verifyFunctionUnits(plan, plans); err == nil || !strings.Contains(err.Error(), "outlives container anchor") {
			t.Fatalf("callee allowed to invalidate another borrowed argument: %v", err)
		}
		return
	}
	t.Fatal("no call")
}

func TestLoopPhiTransfersUnitOnEveryIteration(t *testing.T) {
	f := newFunc("loop", ast.ArrayType{Elem: ast.StringType{}})
	array := f.addParam(f.result, ParamCounted, ast.Position{})
	cond := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	entry, header, body, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBr(entry, header)
	f.graph.SetBrIf(header, cond, body, exit)
	f.graph.SetBr(body, header)
	value := f.addPhi(header, f.result, ast.Position{}, array, array)
	header.Ops[0].Args[1] = value
	f.graph.SetRet(exit, value)
	p := verifiedUnitPlan(t, f)
	for _, edge := range []flowEdge{{entry, header}, {body, header}} {
		step := p.edges[edge]
		if len(step.supplies) != 1 || step.supplies[0].mode != unitMove || len(step.drops) != 0 {
			t.Fatalf("loop edge invented or leaked units: %+v", step)
		}
	}
}

func TestUnitVerifierRejectsMissingLifetimeAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*functionUnits, *ssa.Block, *ssa.Block, ssa.Value)
		want string
	}{
		{"missing-phi-unit", func(p *functionUnits, from, to *ssa.Block, _ ssa.Value) {
			s := p.edges[flowEdge{from, to}]
			s.supplies = nil
			p.edges[flowEdge{from, to}] = s
		}, "supplies disagree"},
		{"early-parent-drop", func(p *functionUnits, from, _ *ssa.Block, parent ssa.Value) {
			op := from.Ops[len(from.Ops)-1]
			s := p.ops[op]
			s.drops = append(s.drops, parent)
			p.ops[op] = s
		}, "outlives container anchor"},
		{"missing-parent-drop", func(p *functionUnits, from, to *ssa.Block, _ ssa.Value) {
			s := p.edges[flowEdge{from, to}]
			s.drops = nil
			p.edges[flowEdge{from, to}] = s
		}, "successor's counted-unit invariant"},
		{"false-immortal", func(p *functionUnits, from, to *ssa.Block, _ ssa.Value) {
			s := p.edges[flowEdge{from, to}]
			s.supplies[0].mode = unitImmortal
			p.edges[flowEdge{from, to}] = s
		}, "not certified immortal"},
		{"return-leak", func(p *functionUnits, _, to *ssa.Block, _ ssa.Value) {
			s := p.returns[to]
			s.supplies[0].mode = unitRetain
			p.returns[to] = s
		}, "unreleased"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, yes, _, join, left, _ := branchProjectionUnits(t)
			tc.edit(p, yes, join, left)
			if err := verifyFunctionUnits(p, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("verify = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestUnitEdgeReleasesOnlyTheUnusedBranch(t *testing.T) {
	p, plans := checkedUnitPlans(t, `
function pilot(own items: string[], choose: boolean): string {
  if (choose) { return items[0]; }
  return "other";
}`)
	f := p.funcs[0]
	plan := plans[f]
	entry := f.graph.Entry
	var released, preserved int
	for _, succ := range entry.Succs() {
		step := plan.edges[flowEdge{entry, succ}]
		if slices.Contains(step.drops, f.graph.Params[0]) {
			released++
		} else {
			preserved++
		}
	}
	if released != 1 || preserved != 1 {
		t.Fatal("branch-only use did not get edge-specific cleanup")
	}
}

func TestLoopPhiAssignmentsAreSimultaneous(t *testing.T) {
	arrayType := ast.ArrayType{Elem: ast.StringType{}}
	f := newFunc("swap", ast.TupleType{Elems: []ast.Type{arrayType, arrayType}})
	left := f.addParam(arrayType, ParamCounted, ast.Position{})
	right := f.addParam(arrayType, ParamCounted, ast.Position{})
	cond := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	entry, header, body, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBr(entry, header)
	f.graph.SetBrIf(header, cond, body, exit)
	f.graph.SetBr(body, header)
	a := f.addPhi(header, arrayType, ast.Position{}, left, left)
	b := f.addPhi(header, arrayType, ast.Position{}, right, right)
	header.Ops[0].Args[1], header.Ops[1].Args[1] = b, a
	result := f.addOp(exit, ssa.OpTupleMake, f.result, ast.Position{}, a, b)
	f.graph.SetRet(exit, result)
	p := verifiedUnitPlan(t, f)
	step := p.edges[flowEdge{body, header}]
	if len(step.supplies) != 2 || step.supplies[0].mode != unitMove || step.supplies[1].mode != unitMove || len(step.drops) != 0 {
		t.Fatalf("loop swap did not transfer both old units before rebinding: %+v", step)
	}
}

func TestDuplicatePhiInputsNeedIndependentUnits(t *testing.T) {
	arrayType := ast.ArrayType{Elem: ast.StringType{}}
	f := newFunc("duplicate", ast.TupleType{Elems: []ast.Type{arrayType, arrayType}})
	input := f.addParam(arrayType, ParamCounted, ast.Position{})
	entry, next := f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBr(entry, next)
	a := f.addPhi(next, arrayType, ast.Position{}, input)
	b := f.addPhi(next, arrayType, ast.Position{}, input)
	result := f.addOp(next, ssa.OpTupleMake, f.result, ast.Position{}, a, b)
	f.graph.SetRet(next, result)
	p := verifiedUnitPlan(t, f)
	step := p.edges[flowEdge{entry, next}]
	if len(step.supplies) != 2 || step.supplies[0].mode != unitRetain || step.supplies[1].mode != unitMove {
		t.Fatalf("duplicate phi inputs shared a single unit: %+v", step)
	}
}

func TestAppendUnitPlanPreservesCopiedChildObligation(t *testing.T) {
	p, plans := checkedUnitPlans(t, `function pilot(items: string[]): string[] { return items.append(items[0]); }`)
	plan := plans[p.funcs[0]]
	for op, step := range plan.ops {
		if op.Kind != ssa.OpArrayAppend {
			continue
		}
		if !slices.Equal(step.copyElements, []ssa.Value{op.Args[0]}) || len(step.supplies) != 1 || step.supplies[0].slot != 1 {
			t.Fatalf("copied children conflated with source buffer ownership: %+v", step)
		}
		step.copyElements = nil
		plan.ops[op] = step
		if err := verifyProgramUnits(p, plans); err == nil {
			t.Fatal("append with no copied child units accepted")
		}
		return
	}
	t.Fatal("no append operation")
}

func TestUnitCertificationChecksTheCalleeNotJustItsSignature(t *testing.T) {
	p, plans := checkedUnitPlans(t, `
function pilot(items: string[]): string { return get(items); }
function get(items: string[]): string { return items[0]; }
`)
	callee := plans[p.funcs[1]]
	for block, step := range callee.returns {
		step.supplies = nil
		callee.returns[block] = step
	}
	if err := verifyProgramUnits(p, plans); err == nil || !strings.Contains(err.Error(), "supplies disagree") {
		t.Fatalf("callee's unowned return accepted: %v", err)
	}
	delete(plans, p.funcs[1])
	if err := verifyProgramUnits(p, plans); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("missing callee plan accepted: %v", err)
	}
}

func TestUnitVerifierIgnoresTamperedCachedLifetimeFacts(t *testing.T) {
	p, yes, _, join, _, _ := branchProjectionUnits(t)
	edge := flowEdge{yes, join}
	step := p.edges[edge]
	child := step.supplies[0].value
	p.lifetime.immortal[child.ID] = true
	step.supplies[0].mode = unitImmortal
	p.edges[edge] = step
	if err := verifyFunctionUnits(p, nil); err == nil || !strings.Contains(err.Error(), "not certified immortal") {
		t.Fatalf("verifier trusted planner's corrupted ownership facts: %v", err)
	}
}
