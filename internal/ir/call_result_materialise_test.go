package ir

import (
	"testing"
)

// #6036, half one: the #4873 grow-containment bracket is skipped for an
// argument that provably does not survive the call. callArgDeaths recognised
// only the self-reassign (`x = f(.., x, ..)`) and direct return-argument
// (`return f(.., x, ..)`) shapes, so `var t = f(b, v); return t;` — where `b`
// is read exactly once in the whole function and never again — was bracketed
// with an rc-inc/rc-dec pair purely to force the callee onto its copy path.
// One full-buffer copy per call, and the caller is a loop.
//
// The bracket is the only rc-inc source in these functions (i32 elements need
// no element retain, and none of them returns a bare param), so a whole-
// function OpRcInc count pins the property at both pointer widths.
func TestSoleOccurrenceArgSkipsGrowBracket(t *testing.T) {
	src := `function f(b: i32[], v: i32): i32[] { return b.append(v); }
function intolocal(b: i32[], v: i32): i32[] {
    var t: i32[] = f(b, v);
    return t;
}
function nestedarg(b: i32[], v: i32): i32[] {
    return f(f(b, v), v + 1);
}
function main(): i32 { return 0; }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		for _, fn := range []string{"intolocal", "nestedarg"} {
			if n := countRcIncs(prog, fn); n != 0 {
				t.Errorf("ptrW=%d: %s emitted %d rc-incs, want 0 — the grow bracket fired on an "+
					"argument that dies at the call, so every append copies the whole buffer (#6036)", ptrW, fn, n)
			}
		}
	}
}

// The other half of the same rule: a sole TEXTUAL occurrence inside a loop is
// still many dynamic reads. `b` is read once per iteration and the binding is
// never overwritten, so an unbracketed in-place grow would be observed by the
// NEXT iteration — the interpreter, which always copies, would disagree. The
// bracket must survive there, which is what keeps the rule from being the
// "textually-last occurrence" heuristic callArgDeaths deliberately rejects.
func TestSoleOccurrenceArgInLoopKeepsGrowBracket(t *testing.T) {
	src := `function f(b: i32[], v: i32): i32[] { return b.append(v); }
function looped(b: i32[], n: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: i32[] = f(b, i);
        total = total + t.len();
        i = i + 1;
    }
    return total;
}
function main(): i32 { return 0; }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if n := countRcIncs(prog, "looped"); n == 0 {
			t.Errorf("ptrW=%d: looped emitted no rc-inc — the grow bracket was dropped for an "+
				"argument whose single textual read re-executes, so the next iteration can "+
				"observe an in-place grow the interpreter never performs", ptrW)
		}
	}
}

// #6036, half two: the exit half of the consumed-array-param ownership-flag
// protocol. A reassignment from a CALL whose result may alias a param clears
// the static freeEligible taint, and the sweep was gated on it — so the flag
// was set, the reassign's overwrite dec fired, the return-transfer inc fired,
// and the matching exit dec was silently dropped. One leaked reference per
// call makes the accumulator permanently shared, and every later append
// copies.
//
// A static dec COUNT cannot express this: the reassignment's overwrite dec
// emits one release per branch of its same-pointer test, only one of which
// ever runs. What distinguishes the two versions is ORDER — the exit release
// is the one that follows the return-transfer retain. So: `threaded` must
// retain `b` on the way out and then release it under the ownership flag,
// while `borrowed` never reassigns, has no flag, and must release nothing —
// the caller still owns that buffer.
func TestConsumedArrayParamSweptDespiteCallTaint(t *testing.T) {
	src := `function f(b: i32[], v: i32): i32[] { return b.append(v); }
function threaded(b: i32[], v: i32): i32[] {
    b = f(b, v);
    return b;
}
function borrowed(b: i32[], v: i32): i32 {
    return b.len() + v;
}
function main(): i32 { return 0; }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		retain, release := lastRetainIdx(prog, "threaded"), lastReleaseIdx(prog, "threaded")
		if retain < 0 {
			t.Fatalf("ptrW=%d: threaded emitted no return-transfer retain at all; the test can no "+
				"longer tell a balanced function from an unlowered one", ptrW)
		}
		if release < retain {
			t.Errorf("ptrW=%d: threaded's last release is at op %d, before the return-transfer "+
				"retain at op %d — the ownership flag's exit dec is missing, so the accumulator "+
				"leaks one reference per call and every later append copies (#6036)",
				ptrW, release, retain)
		}
		if idx := lastReleaseIdx(prog, "borrowed"); idx >= 0 {
			t.Errorf("ptrW=%d: borrowed released at op %d, want no release — a param that is only "+
				"READ belongs to the caller, and releasing it underflows the caller's count", ptrW, idx)
		}
	}
}

// lastRetainIdx / lastReleaseIdx return the index of the last retain / release
// op in `fn`, or -1. A release is either the dedicated rc-dec op or the
// __fern_arr_dec call the array arm of the exit sweep emits.
func lastRetainIdx(p *Program, fn string) int {
	return lastOpIdx(p, fn, func(op Op) bool { return op.Kind == OpRcInc })
}

func lastReleaseIdx(p *Program, fn string) int {
	return lastOpIdx(p, fn, func(op Op) bool {
		return op.Kind == OpRcDec || (op.Kind == OpCallDirect && op.Str == "__fern_arr_dec")
	})
}

func lastOpIdx(p *Program, fn string, match func(Op) bool) int {
	idx := -1
	for _, f := range p.Funcs {
		if f.Name != fn {
			continue
		}
		for i, op := range f.Ops {
			if match(op) {
				idx = i
			}
		}
	}
	return idx
}
