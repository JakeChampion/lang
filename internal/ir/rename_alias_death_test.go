package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A state-threading function that opens by renaming its parameter —
// `var c: C = c0;`, the line most of the self-host lowering starts with — used
// to pay a full-buffer copy at every append below it (#8498). Two independent
// mechanisms had to hold for the rename to cost nothing:
//
//   - the binding MOVES rather than retains, so c0's reference does not sit
//     alive to the exit sweep keeping the container at rc 2;
//   - callArgDeaths admits the renamed local on its source's footing, so the
//     calls it threads skip #4873's containment bracket.
//
// Either one missing leaves the chain quadratic: three appends around one call
// took 3.0 s against 2 ms for the same work written without the rename.
const renameAliasSrc = `struct C { insts: i32[], n: i32 }
function emit(c: C, v: i32): C { return C { ...c, insts: c.insts.append(v), n: c.n + 1 }; }
function renameOnly(c0: C): C { var c: C = c0; return C { ...c, n: c.n + 1 }; }
function renameSourceLive(c0: C): C { var c: C = c0; return C { ...c, n: c.n + c0.n }; }
function threaded(c0: C): C {
    var c: C = c0;
    var open: C = emit(c, 1);
    return emit(open, 2);
}
function threadedSourceLive(c0: C): i32 {
    var c: C = c0;
    var open: C = emit(c, 1);
    return open.n + c0.insts.len();
}
function main(): i32 {
    var a: C = C { insts: [], n: 0 };
    return renameOnly(a).n + renameSourceLive(a).n + threaded(a).n + threadedSourceLive(a);
}`

func TestRenamedParamAliasMoves(t *testing.T) {
	ip := lowerForTest(t, renameAliasSrc)
	if n := incsBeforeFirstCall(fnNamed(t, ip, "renameOnly")); n != 0 {
		t.Errorf("renameOnly retains for `var c: C = c0` (%d rc_inc before its first call); c0 is never named again, so the alias should take its reference:\n%s", n, ip)
	}
	// Anti-vacuity: the source read after the rename keeps both names live,
	// and there the transfer inc is what the exit sweep's second dec pairs
	// with.
	if n := incsBeforeFirstCall(fnNamed(t, ip, "renameSourceLive")); n == 0 {
		t.Errorf("renameSourceLive moves out of c0 while c0 is still read afterwards:\n%s", ip)
	}
}

func TestRenamedParamAliasNotBracketed(t *testing.T) {
	ip := lowerForTest(t, renameAliasSrc)
	if n := incsBeforeCall(fnNamed(t, ip, "threaded"), "emit"); n != 0 {
		t.Errorf("threaded brackets the renamed cursor around emit (%d rc_inc before the call); the rename is c0's only occurrence:\n%s", n, ip)
	}
	if n := incsBeforeCall(fnNamed(t, ip, "threadedSourceLive"), "emit"); n == 0 {
		t.Errorf("threadedSourceLive does NOT bracket the cursor, but c0 reads the same buffers after the call:\n%s", ip)
	}
}

// incsBeforeFirstCall counts the OpRcInc ops preceding a function's first
// call. The alias binding is the first statement of both functions below, so
// that prefix holds its transfer retain and nothing else.
func incsBeforeFirstCall(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect {
			break
		}
		if op.Kind == ir.OpRcInc {
			n++
		}
	}
	return n
}
