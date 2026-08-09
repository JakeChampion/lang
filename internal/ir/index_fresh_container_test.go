package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Indexing a FRESH owned array — `mk()[0]` — reclaims the container once the
// element is loaded. That already held for a scalar element; an rc-tracked
// POINTER element was excluded, because the loaded value aliases the buffer
// about to be freed. Excluded meant the container was dropped on the floor:
// `mk_strs()[0].len()` leaked the spine and every element it did not extract,
// 304 B a round with no plateau, where `var xs = mk_strs(); xs[0].len()` was
// flat. The fix is the pair the fresh-container field read already uses
// (#6401) — retain the element, then deep-drop the container, which nets the
// element to the expression's own single reference.
//
// The assertions key on the DROP HELPER name because that is what
// distinguishes the deep drop (which releases each element's buffer) from the
// shallow __fern_arr_dec that would leak them.

// countStringRetains counts the retains emitRetainValueOnStack /
// emitAliasInc can produce for a string, across both string ABIs: the
// two-word targets take __fern_str_inc, native single-word takes OpRcInc.
func countStringRetains(fn *Func) int {
	n := countCallDirect(fn.Ops, "__fern_str_inc")
	for _, op := range fn.Ops {
		if op.Kind == OpRcInc {
			n++
		}
	}
	return n
}

// The elements are fresh concats rather than literals so each one is a real
// heap buffer the container's deep drop has to reach.
const freshIndexSrc = `function mks(n: i32, p: string): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < n) { out = out.append(p + "-elem"); i = i + 1; }
    return out;
}
function borrowed(n: i32, p: string): i32 { return mks(n, p)[0].len(); }
function bound(n: i32, p: string): i32 { var s: string = mks(n, p)[0]; return s.len(); }
function fromlocal(n: i32, p: string): i32 { var xs: string[] = mks(n, p); return xs[0].len(); }
function fromparam(xs: string[]): i32 { return xs[0].len(); }
function main(): i32 { return 0; }`

// The container must be deep-dropped in both shapes that consume a fresh
// array by index — the borrowing `.len()` read and the binding one. Before
// the fix neither emitted a drop of any kind.
func TestIndexOfFreshArrayDropsTheContainer(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, freshIndexSrc, ptrW)
		for _, name := range []string{"borrowed", "bound"} {
			fn := findFunc(p, name)
			if n := countCallDirect(fn.Ops, "__fern_drop_arr_str"); n == 0 {
				t.Errorf("ptrW=%d: %s never drops the array returned by mks — the spine "+
					"and every unextracted element leak once per call; ops:\n%s", ptrW, name, p)
			}
		}
	}
}

// The extracted element is retained before the container's deep drop reaches
// it. Without the retain the drop frees the string out from under the value
// the expression just produced — a use-after-free, not a leak.
func TestIndexOfFreshArrayRetainsTheElement(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, freshIndexSrc, ptrW)
		fn := findFunc(p, "borrowed")
		if n := countStringRetains(fn); n != 1 {
			t.Errorf("ptrW=%d: borrowed emitted %d string retains, want exactly 1 — the "+
				"element must be retained before the container's deep drop decs it; ops:\n%s",
				ptrW, n, p)
		}
	}
}

// The binding site must treat the read as a MOVE. The read already netted the
// element to one reference; the usual alias inc would be a second, and the
// exit sweep's single dec then leaves it at rc 1 forever — the container's
// bytes come back and the element's do not.
func TestIndexOfFreshArrayBindsWithoutASecondInc(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, freshIndexSrc, ptrW)
		fn := findFunc(p, "bound")
		if n := countStringRetains(fn); n != 1 {
			t.Errorf("ptrW=%d: bound emitted %d string retains, want exactly 1 — the "+
				"lowering's retain plus the binding's alias inc is one too many; ops:\n%s",
				ptrW, n, p)
		}
	}
}

// The other direction, and the one that would be a miscompile rather than a
// leak: an array named by a LOCAL or a PARAM is not this expression's to free.
// Dropping it at the index would release a buffer its owner still holds.
func TestIndexOfABorrowedArrayDropsNothing(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, freshIndexSrc, ptrW)
		fn := findFunc(p, "fromparam")
		if n := countCallDirect(fn.Ops, "__fern_drop_arr_str"); n != 0 {
			t.Errorf("ptrW=%d: fromparam dropped an array it borrows from its caller (%d "+
				"drops) — use-after-free; ops:\n%s", ptrW, n, p)
		}
		if n := countStringRetains(fn); n != 0 {
			t.Errorf("ptrW=%d: fromparam retained an element of a borrowed array (%d "+
				"retains) — an unbalanced inc leaks it; ops:\n%s", ptrW, n, p)
		}
		// A local container is dropped by the `var` machinery, not by the
		// index: the NULL-guarded reinit drop before the init store and the
		// exit sweep, two in total. A third would mean the index reclaimed a
		// container the local still owns.
		local := findFunc(p, "fromlocal")
		if n := countCallDirect(local.Ops, "__fern_drop_arr_str"); n != 2 {
			t.Errorf("ptrW=%d: fromlocal emitted %d container drops, want 2 (the reinit "+
				"guard and the exit sweep) — the index must not add its own; ops:\n%s",
				ptrW, n, p)
		}
	}
}

// Free-off lowering stays byte-identical: the whole reclaim path is gated on
// ast.RcFreeEnabled through freshOwnedRcTempType / ownedCallResultType, which
// is what keeps the *FixturesFreeMatchesNoFree gates meaningful.
func TestIndexOfFreshArrayEmitsNothingWithFreeOff(t *testing.T) {
	defer func(prev bool) { ast.RcFreeEnabled = prev }(ast.RcFreeEnabled)
	ast.RcFreeEnabled = false
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, freshIndexSrc, ptrW)
		fn := findFunc(p, "borrowed")
		if n := countCallDirect(fn.Ops, "__fern_drop_arr_str"); n != 0 {
			t.Errorf("ptrW=%d: borrowed emitted %d drops with reclamation off; ops:\n%s",
				ptrW, n, p)
		}
	}
}
