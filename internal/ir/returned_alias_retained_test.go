package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
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

// noOwnedParams is the ownership fact for a program with no owned-by-default
// parameter: every bare parameter return is then refused.
func noOwnedParams(*ast.FuncDecl, int) bool { return false }

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
	return findReturnsFreshBox(prog, info, map[string]bool{}, map[string]bool{}, noOwnedParams)
}

// An OWNED-BY-DEFAULT parameter returned bare is credited: the callee's exit
// sweep releases the reference it was handed, so the transfer inc is the
// caller's own. `Tip => return t` is the arm every tree rebuild ends in, and
// refusing it stranded one node per rebuild in the set algebra. The verdict
// is the lowering's own ladder, so a borrowed parameter — an escaping Map,
// the threaded accumulator the refusal exists for — stays refused.
func TestReturnedOwnedByDefaultParamIsCredited(t *testing.T) {
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
	facts := paramVerdictFacts{info: info, ptrW: 8, paramEscapes: inferParamEscapes(prog, info)}
	q := findReturnsFreshBox(prog, info, map[string]bool{}, map[string]bool{}, facts.ownedParam)
	if !q["walk"] {
		t.Error("returnsFreshBox[walk] = false, want true — `return t` of an owned-by-default enum " +
			"parameter hands the caller the reference the callee's sweep would otherwise balance")
	}
	if q["thread"] {
		t.Error("returnsFreshBox[thread] = true, want false — a Map parameter is borrowed, and the " +
			"bare-parameter credit is refused for a borrow")
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

// A bare BORROWED parameter return is refused even though it takes the same
// inc: a threaded accumulator (`m = f(m, …)`) reaches a callee whose
// ownership-flag protocol declines to release the parameter, so nothing
// balances the inc and the value gains a reference per call. Crediting it
// leaked url.query_parse outright — 5 blocks on a single `query_parse("a=1")`.
func TestReturnedBareParamIsRefusedTheAliasCredit(t *testing.T) {
	if q := freshBoxFor(t, retainedSrc); q["pick_param"] {
		t.Error("returnsFreshBox[pick_param] = true, want false — a threaded " +
			"parameter's rebind may decline the dec that balances the return inc")
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
		if findReturnsFreshBox(prog, info, tc.pair, tc.trm, noOwnedParams)["pick_index"] {
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
