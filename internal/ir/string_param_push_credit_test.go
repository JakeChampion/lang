package ir

import "testing"

// #7914 frontier: a string parameter stored via `xs.append(p)` is a COUNTED
// store — emitArrayPush emits the element retain unconditionally for a
// pointer element, and the buffer's deep drop gives it back — so the
// parameter is counted-retain and every caller's fresh argument temp gets
// its stage-(b) release. The array tier gained this position in #7867
// slices 1 and 4; the string tier never had it, which is what stranded a
// fresh string per derived-scope construction in the checker (one 64 B
// temp per `child(w(...))` call, measured).

func TestStringParamPushedElementIsCounted(t *testing.T) {
	src := `function keep(xs: string[], nm: string): string[] {
    return xs.append(nm);
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "keep")
	if len(got) != 2 || !got[1] {
		t.Errorf("paramCountedRetain[keep] = %v, want [_ true] — the push emits the "+
			"element retain unconditionally, so the store is counted", got)
	}
}

func TestStringParamPushedThenReturnedBareStaysUncredited(t *testing.T) {
	src := `function keep(xs: string[], nm: string): string {
    var ys: string[] = xs.append(nm);
    if (ys.len() > 99) { return "x"; }
    return nm;
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "keep")
	if len(got) == 2 && got[1] {
		t.Errorf("paramCountedRetain[keep] = %v, but the bare `return nm` hands out "+
			"a reference nothing counts — crediting it double-frees the caller's temp", got)
	}
}

func TestStringParamForwardedToAPushingCalleeIsCounted(t *testing.T) {
	src := `function keep(s: string): string[] {
    var out: string[] = [];
    out = out.append(s);
    return out;
}
function forward(s: string): string[] { return keep(s); }
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "forward")
	if len(got) != 1 || !got[0] {
		t.Errorf("paramCountedRetain[forward] = %v, want [true] — forwarding to a "+
			"counted position retains nothing new (the summary fixpoint credits the "+
			"chain once keep's push store grounds it)", got)
	}
}
