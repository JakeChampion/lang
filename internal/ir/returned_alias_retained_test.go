package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// returnsOwnBox used to refuse every returned ALIAS — a bare ident, a field
// read, an index — on the premise that `return p` hands back the caller's box.
// The Return lowering stopped making that true: needsRcIncOnAlias covers every
// rcTrackedSlotType and does not ask whether the aliased base is a local or a
// parameter, so the caller receives a reference of its own. rhsTainted was the
// only consumer, and it kept tainting those results, so no caller dropped one.

const retainedSrc = `
struct Reg { names: string[] }
function pick_index(r: Reg, i: i32): string { return r.names[i]; }
function pick_field(r: Reg): string[] { return r.names; }
function pick_local(r: Reg, i: i32): string { var t: string = r.names[i]; return t; }
function pick_param(s: string): string { return s; }
function scalar_param(n: i32): i32 { return n; }
function main(): i32 {
    var r: Reg = Reg { names: ["aa", "bb"] };
    return pick_index(r, 0).len() + pick_field(r).len() +
        pick_local(r, 0).len() + pick_param("z").len() + scalar_param(1);
}`

func freshBoxFor(t *testing.T, src string) map[string]bool {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return findReturnsFreshBox(prog, info, map[string]bool{}, map[string]bool{})
}

// A parameter returned bare is credited whether it is owned-by-default or
// borrowed: the transfer inc makes the caller's reference its own. `Tip =>
// return t` is the arm every tree rebuild ends in, and refusing it stranded
// one node per rebuild in the set algebra; `thread` is the borrowed Map the
// refusal used to exist for (#7914).
func TestReturnedBareParamIsCredited(t *testing.T) {
	src := `
enum Node { Tip, Bin(Node, i32, Node) }
function walk(t: Node): Node {
    match (t) {
        Tip => { return t; },
        Bin(l, k, r) => { return Bin(walk(l), k, walk(r)); }
    }
}
function thread(m: Map[i32, i32], k: i32): Map[i32, i32] { m = m.insert(k, 1); return m; }
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    match (walk(Bin(Tip, 1, Tip))) {
        Tip => { return 1; },
        Bin(l, k, r) => { return thread(m, k).len() - 1; }
    }
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	q := findReturnsFreshBox(prog, info, map[string]bool{}, map[string]bool{})
	q["pick_param"] = freshBoxFor(t, retainedSrc)["pick_param"]
	for _, fn := range []string{"walk", "thread", "pick_param"} {
		if !q[fn] {
			t.Errorf("returnsFreshBox[%s] = false, want true — the Return lowering inc's a bare "+
				"parameter on the way out, so the caller owns the reference it receives", fn)
		}
	}
}

func TestReturnedAliasCountsAsAFreshBox(t *testing.T) {
	q := freshBoxFor(t, retainedSrc)
	for _, name := range []string{"pick_index", "pick_field", "pick_local"} {
		if !q[name] {
			t.Errorf("returnsFreshBox[%s] = false, want true — the Return lowering "+
				"inc's this alias on the way out, so the caller owns it", name)
		}
	}
}

// A scalar return carries no reference to transfer, so crediting it would be
// meaningless rather than merely generous: rcTrackedSlotType is the gate.
func TestScalarReturnEarnsNoAliasCredit(t *testing.T) {
	if q := freshBoxFor(t, retainedSrc); q["scalar_param"] {
		t.Error("returnsFreshBox[scalar_param] = true, want false — an i32 return " +
			"takes no transfer inc, so there is nothing to own")
	}
}

// Two rewrites reach a return before the transfer inc does. Neither may earn
// the credit, and both are refused by name rather than by shape.
func TestPairFormAndTrmcAreRefusedTheAliasCredit(t *testing.T) {
	prog, err := parser.Parse(retainedSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, tc := range []struct {
		what      string
		pair, trm map[string]bool
	}{
		{"pair-form", map[string]bool{"pick_index": true}, map[string]bool{}},
		{"trmc", map[string]bool{}, map[string]bool{"pick_index": true}},
	} {
		if findReturnsFreshBox(prog, info, tc.pair, tc.trm)["pick_index"] {
			t.Errorf("%s: returnsFreshBox[pick_index] = true, want false — that "+
				"rewrite returns before the transfer inc is emitted", tc.what)
		}
	}
}

// The credit is only sound because the lowering really does emit the inc. Pin
// that at the op level, at both pointer widths, so a change to the Return
// lowering that dropped it fails here instead of silently freeing a borrow.
func TestReturnedAliasEmitsTheTransferInc(t *testing.T) {
	prog, err := parser.Parse(retainedSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, ptrW := range []int{4, 8} {
		p, err := LowerWith(prog, info, ptrW)
		if err != nil {
			t.Fatalf("ptrW=%d: lower: %v", ptrW, err)
		}
		for _, want := range []string{"pick_index", "pick_field"} {
			fn := funcNamed(p, want)
			if fn == nil {
				t.Fatalf("ptrW=%d: no lowered func %s", ptrW, want)
			}
			incs := 0
			for _, op := range fn.Ops {
				if op.Kind == OpRcInc || (op.Kind == OpCallDirect && op.Str == "__fern_str_inc") {
					incs++
				}
			}
			if incs == 0 {
				t.Errorf("ptrW=%d: %s emits no retain before its return — the "+
					"returnsFreshBox credit for a returned alias rests on it", ptrW, want)
			}
		}
	}
}

func funcNamed(p *Program, name string) *Func {
	for _, f := range p.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}
