package ssa

import (
	"reflect"
	"testing"
)

// TestLivenessAcrossManyValues — more values than one word of the live set
// holds, all defined before a loop and all read after it, so every one is
// live through the loop's header and body on both sides of the back edge.
func TestLivenessAcrossManyValues(t *testing.T) {
	const n = 150
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()

	var vals []Value
	for i := 0; i < n; i++ {
		vals = append(vals, f.AddOp(entry, OpConstInt))
	}
	f.SetBr(entry, header)
	f.SetBrIf(header, cond, body, exit)
	f.AddOp(body, OpAdd, vals[0], vals[n-1])
	f.SetBr(body, header)
	sum := vals[0]
	for _, v := range vals[1:] {
		sum = f.AddOp(exit, OpAdd, sum, v)
	}
	f.SetRet(exit, sum)

	want := []int32{cond.ID}
	for _, v := range vals {
		want = append(want, v.ID)
	}
	l := ComputeLiveness(f)
	for name, got := range map[string][]int32{
		"LiveOut(entry)": l.LiveOutSorted(entry),
		"LiveIn(header)": l.LiveInSorted(header),
		"LiveIn(body)":   l.LiveInSorted(body),
		"LiveOut(body)":  l.LiveOutSorted(body),
	} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s has %d values, want all %d: %v", name, len(got), len(want), got)
		}
	}
	if got := l.LiveInSorted(exit); !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("LiveIn(exit) = %v, want the %d values and not the condition", got, n)
	}
}
