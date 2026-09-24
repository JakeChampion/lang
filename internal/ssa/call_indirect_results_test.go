package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// A call through a function VALUE leaves as many values as its result has
// words, and the lift has to agree with the verifier about that: nothing for a
// void callee (#8539), a pair for a two-word one (#8643).
//
// #8504 stopped the IR emitting an OpDrop after such a call — correctly, since
// a void `call_indirect` pushes nothing. The lift still credited the op with
// one result, so from that point every stack height was one too high. It never
// broke code generation, because DCE removed the dead value; it showed up only
// once the two stack models were compared per op, where it read as
// `run op[2] call_indirect: verifier 0, lift 1`.
//
// The direct-call path had already been through exactly this (see the
// `voidCallee` branch in lift.go); this is the indirect twin.
//
// Lowering real source rather than hand-building IR is deliberate: the bug was
// in reading the call's shape, so the test has to go through the same
// CallShapes the compiler builds.
func TestCallIndirectLiftMatchesVerifierResultCount(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// wantResults is how many values the call_indirect leaves.
		wantResults int
		// twoWord lowers under the two-word string ABI (arm64, wasm32).
		twoWord bool
	}{
		{
			name:        "void callee leaves nothing",
			wantResults: 0,
			src: `function run(f: (i32) => void, v: i32): void { f(v); }
function main(): i32 {
    var seen: i32 = 0;
    var g = (x: i32) => { seen = seen + x; };
    run(g, 4);
    return seen - 4;
}`,
		},
		{
			name:        "value-returning callee still leaves one",
			wantResults: 1,
			src: `function run(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var g = (x: i32) => x + 1;
    return run(g, 4) - 5;
}`,
		},
		{
			// A string under the two-word ABI is two words, so the call
			// leaves a pair (#8643).
			name:        "two-word string callee leaves a pair",
			wantResults: 2,
			twoWord:     true,
			src: `function run(f: () => string): string { return f(); }
function main(): i32 {
    var g = () => "hi";
    var s = run(g);
    if (s == "hi") { return 0; }
    return 1;
}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(tc.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v", err)
			}
			prev := ast.TwoWordOverride
			ast.TwoWordOverride = tc.twoWord
			ip, err := ir.LowerWith(prog, info, 8)
			ast.TwoWordOverride = prev
			if err != nil {
				t.Fatalf("lower: %v", err)
			}
			shapes := ir.NewCallShapes(ip)

			var runFn *ir.Func
			for _, fn := range ip.Funcs {
				if fn.Name == "run" {
					runFn = fn
				}
			}
			if runFn == nil {
				t.Fatal("no `run` function in the lowered program")
			}

			// The verifier's own count, which the lift must reproduce.
			var indirect ir.Op
			var found bool
			for _, op := range runFn.Ops {
				if op.Kind == ir.OpCallIndirect {
					indirect, found = op, true
					break
				}
			}
			if !found {
				t.Fatal("`run` lowered without an OpCallIndirect")
			}
			gotVerifier, bail := shapes.ResultSlots(indirect)
			if bail != "" {
				t.Fatalf("ResultSlots bailed: %s", bail)
			}
			if gotVerifier != tc.wantResults {
				t.Fatalf("the verifier says %d result(s), want %d — the premise of this test is wrong",
					gotVerifier, tc.wantResults)
			}

			f, err := LiftFromIRWith(runFn, shapes)
			if err != nil {
				t.Fatalf("LiftFromIRWith: %v", err)
			}
			var lifted *Op
			for _, bb := range f.Blocks {
				for _, op := range bb.Ops {
					if op.Kind == OpCallIndirect {
						lifted = op
					}
				}
			}
			if lifted == nil {
				t.Fatal("the lift produced no OpCallIndirect")
			}
			gotLift := 0
			if lifted.Result.IsValid() {
				gotLift++
			}
			if lifted.Result2.IsValid() {
				gotLift++
			}
			if gotLift != tc.wantResults {
				t.Errorf("the lift leaves %d result(s), the verifier %d — every stack height after this op is off by %d",
					gotLift, gotVerifier, gotLift-gotVerifier)
			}
		})
	}
}
