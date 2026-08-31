package ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

// Every synthesised drop thunk takes one heap pointer and releases it,
// and the solver must be able to SEE that: the thunk's parameter must
// lift as an address (ParamAddrs, from the declared `usize`) and settle
// Consumed on its own evidence. Before #7866 the thunks declared the
// parameter as a bare number, ParamAddrs[0] read false, and the solver
// never asked — `generatedDropPrefixes` masked the gap for every member
// it named, and the first generated drop missing from that list shipped
// two false leak findings (`__closure_drop_`, #7865).
//
// This is the cross-check that turns the table's redundancy into a
// gate: a NEW drop family that forgets the pointer-typed parameter now
// fails here, whether or not anyone remembers to add it to the table.
func TestGeneratedDropThunksLiftAsConsumedPointerTakers(t *testing.T) {
	src := `
struct Node { name: string, n: i32 }
enum Msg { Text(string), Code(i32) }
function mknode(s: string): Node { return Node { name: s, n: 1 }; }
function main(): i32 {
    var pad: string = "xyzw";
    var nodes: Node[] = [];
    nodes = nodes.append(mknode(pad + "0123456789abcdef"));
    var m: Msg = Msg.Text(pad + "fedcba9876543210");
    var pair: (string, i32) = (pad + "aaaabbbbccccdddd", 2);
    var got: i32 = 0;
    match (m) { Text(s) => { got = s.len(); }, Code(c) => { got = c; } }
    return nodes.len() + pair.1 + got - 21;
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	ip, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}

	funcs, failures := ssa.LiftProgram(ip)
	for _, lf := range failures {
		if isGeneratedDropName(lf.Func) {
			t.Errorf("drop thunk %s failed to lift: %v", lf.Func, lf.Err)
		}
	}
	sol := ssa.SolveOwnership(funcs)

	var checked []string
	for name, f := range funcs {
		if !isGeneratedDropName(name) {
			continue
		}
		checked = append(checked, name)
		sig := sol.Sigs[name]
		if len(sig.Pointer) == 0 || !sig.Pointer[0] {
			t.Errorf("%s: Pointer[0] = false — the thunk's parameter is not declared "+
				"pointer-width, so the solver never examines what the body does to it (#7866)",
				name)
			continue
		}
		if len(f.ParamAddrs) == 0 || !f.ParamAddrs[0] {
			t.Errorf("%s: ParamAddrs[0] = false after the lift", name)
		}
		if len(sig.Params) == 0 || sig.Params[0] != ssa.Consumed {
			t.Errorf("%s: Params[0] = %v, want consumed — the body releases its argument "+
				"on every path, and the solver should derive that on its own", name, sig.Params[0])
		}
	}
	// The families this program is built to generate. Fewer means the
	// lowering stopped synthesising thunks and the loop above checked
	// nothing.
	if len(checked) < 3 {
		t.Fatalf("only %d generated drop thunks reached the solver (%v) — the probe "+
			"program no longer exercises the drop families", len(checked), checked)
	}
}

// isGeneratedDropName matches the synthesised drop families by the same
// closed list the rc signature table uses.
func isGeneratedDropName(name string) bool {
	for _, p := range ir.RcGeneratedDropPrefixes() {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	for _, n := range ir.RcGeneratedDropNames() {
		if name == n {
			return true
		}
	}
	return false
}
