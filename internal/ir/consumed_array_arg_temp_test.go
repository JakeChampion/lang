package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A fresh array temp passed at a CONSUMED-THREADED array parameter is owned by
// nobody without this: the ownership-flag protocol starts at 0 — the slot
// still holds the caller's borrow — so the callee never releases the buffer it
// was handed, and paramCountedRetain refuses the position because the body
// hands the parameter out bare (`return acc`). `fold_all([], items)` leaked its
// 16 B literal on every call.

const foldSrc = `
function visit(st: string, acc: string[]): string[] { return acc.append(st); }
function fold_all(out: string[], items: string[]): string[] {
    let i: i32 = 0;
    while (i < items.len()) { out = visit(items[i], out); i = i + 1; }
    return out;
}
function reads(xs: string[]): i32 { return xs.len(); }
function main(): i32 {
    let items: string[] = ["aa", "bb"];
    let got: string[] = fold_all([], items);
    return got.len() + reads([]);
}`

func consumedArrayPosFor(t *testing.T, src string) map[string][]bool {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return consumedArrayParamPositions(prog, info, map[string]bool{})
}

func TestConsumedArrayPositionsNameTheThreadedAccumulator(t *testing.T) {
	pos := consumedArrayPosFor(t, foldSrc)
	got, ok := pos["fold_all"]
	if !ok || len(got) != 2 {
		t.Fatalf("consumedArrayParamPositions[fold_all] = %v (present=%v), want two entries", got, ok)
	}
	if !got[0] {
		t.Errorf("fold_all's `out` is reassigned from a call and never released by the "+
			"callee, so it is consumed-threaded; got %v", got)
	}
	if got[1] {
		t.Errorf("fold_all's `items` is never reassigned, so it is a plain borrow; got %v", got)
	}
	// A parameter no assignment ever touches is not threaded, so the callee
	// never takes ownership and the caller must NOT release its temp here —
	// the callee is reading a buffer the caller still owns.
	if p, ok := pos["reads"]; ok && len(p) > 0 && p[0] {
		t.Errorf("consumedArrayParamPositions[reads] = %v, but `xs` is read-only", p)
	}
}

// The interlock the whole-program projection rests on: for an ARRAY parameter
// it must agree, function for function, with the per-function analysis the
// lowering actually runs. Func.ParamConsumed records that verdict as
// `own || ownedByDefault || consumedParams`; owned-by-default is always false
// for an array (isOwnedByDefaultType has no ArrayType arm), so on a NON-own
// array parameter the two must match exactly.
//
// An `own` parameter is excluded because ParamConsumed cannot separate the
// disjuncts there, and because the projection deliberately declines it: an
// `own` position is the callee's to reclaim, so the call site suppresses the
// stage-(b) drop through ownedByCalleeAt and admitting it here would free the
// temp twice. The first draft of this test compared them anyway and caught
// that as a drift, which is the test working.
func TestConsumedArrayPositionsMatchTheLoweringVerdict(t *testing.T) {
	for _, src := range []string{foldSrc, overwriteSrc, `
function grow(a: string[], b: string[]): string[] {
    a = a.append("x");
    b = b.with(0, "y");
    return a;
}
function own_thread(own o: string[]): string[] { o = o.append("z"); return o; }
function nested(rows: string[][]): string[][] { rows = rows.append([]); return rows; }
function main(): i32 { return 0; }`} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		want := consumedArrayParamPositions(prog, info, map[string]bool{})
		ip := lowerSourceWith(t, src, 8)
		for _, fn := range ip.Funcs {
			decl := declByName(prog, fn.Name)
			if decl == nil {
				continue
			}
			for i, p := range decl.Params {
				if _, isArr := p.Type.(ast.ArrayType); !isArr || p.Own {
					continue
				}
				if i >= len(fn.ParamConsumed) {
					continue
				}
				w := i < len(want[fn.Name]) && want[fn.Name][i]
				if fn.ParamConsumed[i] != w {
					t.Errorf("%s param %d (%s): lowering says consumed=%v, the "+
						"whole-program projection says %v — the two have drifted",
						fn.Name, i, p.Name, fn.ParamConsumed[i], w)
				}
			}
		}
	}
}

func declByName(prog *ast.Program, name string) *ast.FuncDecl {
	for _, fn := range prog.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}

// The op-level effect, and the guard that makes it safe. The callee can hand
// the temp straight back — nothing rebinds `out` when the loop body never runs
// — so the drop sits behind a pointer-changed test against the call's result.
func TestConsumedArrayArgTempIsStashedAndGuarded(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, foldSrc, ptrW)
		fn := findFunc(p, "main")
		if n := countCallDirect(fn.Ops, "__fern_drop_arr_str"); n == 0 {
			t.Errorf("ptrW=%d: main never releases the fresh array temp it hands to "+
				"fold_all — the callee treats it as a borrow, so nobody does; ops:\n%s",
				ptrW, p)
		}
		var sawNe, sawIf bool
		for _, op := range fn.Ops {
			switch op.Kind {
			case OpNe:
				sawNe = true
			case OpIf:
				sawIf = true
			}
		}
		if !sawNe || !sawIf {
			t.Errorf("ptrW=%d: the temp's drop is unguarded (ne=%v if=%v) — fold_all "+
				"returns `out` unchanged when the loop never runs, and the result is "+
				"then the very temp being freed; ops:\n%s", ptrW, sawNe, sawIf, p)
		}
	}
}

// The callee can hand the temp straight back — its loop never ran, or its
// push kept the pointer — and its bare `return out` carries the return
// transfer inc either way, so the identity case owes one flat dec: without
// it `grow([1, 2, 3], false)` stranded its literal on every call, and the
// serve loop's `__with_backlog(conns, drv.wait(...))` stranded the wait's
// event list on every wait (#10617).
func TestConsumedArrayArgTempIdentityCasePaysTheTransferDec(t *testing.T) {
	const src = `
function grow(a: i32[], more: boolean): i32[] { if (more) { a = a.append(7); } return a; }
function main(): i32 {
    let e: i32[] = grow([1, 2, 3], false);
    return e.len();
}`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		fn := findFunc(p, "main")
		var sawElse, decAfterElse bool
		for _, op := range fn.Ops {
			switch op.Kind {
			case OpElse:
				sawElse = true
			case OpRcDec:
				if sawElse {
					decAfterElse = true
				}
			}
		}
		if !sawElse || !decAfterElse {
			t.Errorf("ptrW=%d: the identity arm of the guarded temp drop has no flat dec "+
				"(else=%v dec=%v), so a temp the callee hands straight back keeps the "+
				"return transfer's count; ops:\n%s", ptrW, sawElse, decAfterElse, p)
		}
	}
}
