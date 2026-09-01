package ir

import "testing"

// `xs.with(i, p)` is `xs.append(p)`'s sibling: for a pointer-shaped
// element emitArraySet incs an aliased element, drops the one it
// overwrites, and the copy path retains through
// __fern_arr_cow_inplace_ptr's element walk — so the buffer owns a
// reference of its own and the parameter is counted-retain.
// computeFreeEligible has read it that way since #4399 sink 2 (its
// Array_set arm routes the source through escapeOwned under exactly
// this gate); the three summary tiers were the half that never did, so
// a helper as small as `put(xs, v) -> xs.with(0, v)` stranded its
// caller's fresh argument, once per call.

func TestStringParamSetIntoArrayIsCounted(t *testing.T) {
	src := `function put(xs: string[], v: string): string[] {
    return xs.with(0, v);
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "put")
	if len(got) != 2 || !got[1] {
		t.Errorf("paramCountedRetain[put] = %v, want [_ true] — the `.with` store incs the "+
			"element, so it is counted", got)
	}
}

func TestArrayElementReadSetIntoArrayIsCounted(t *testing.T) {
	src := `function move(dst: string[], src: string[]): string[] {
    return dst.with(0, src[0]);
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "move")
	if len(got) != 2 || !got[1] {
		t.Errorf("paramCountedRetain[move] = %v, want [_ true] — `src[0]` handed to a "+
			"counted store is a counted occurrence, not a reference handed out", got)
	}
}

func TestStructParamSetIntoArrayIsCounted(t *testing.T) {
	src := `struct Node { name: string, n: i32 }
function put(xs: Node[], v: Node): Node[] {
    return xs.with(0, v);
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "put")
	if len(got) != 2 || !got[1] {
		t.Errorf("paramCountedRetain[put] = %v, want [_ true] — the struct tier takes the "+
			"same store", got)
	}
}

// The refusal that keeps it sound: one counted store does not launder a
// bare hand-out, because everyOccurrenceSafe is all-or-nothing.
func TestStringParamSetThenReturnedBareStaysUncredited(t *testing.T) {
	src := `function put(xs: string[], v: string): string {
    var ys: string[] = xs.with(0, v);
    if (ys.len() > 99) { return "x"; }
    return v;
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "put")
	if len(got) == 2 && got[1] {
		t.Errorf("paramCountedRetain[put] = %v, but `return v` hands out a reference "+
			"nothing counts", got)
	}
}

// The RECEIVER is deliberately not credited: `.with` hands the
// receiver's own buffer back at rc 1, which is a retention the caller
// cannot discount.
func TestArrayParamSetReceiverStaysUncredited(t *testing.T) {
	src := `function put(xs: string[], v: string): string[] {
    return xs.with(0, v);
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "put")
	if len(got) == 2 && got[0] {
		t.Errorf("paramCountedRetain[put] = %v, but the result IS the receiver's buffer "+
			"when it is unique — crediting it lets the caller free a live one", got)
	}
}
