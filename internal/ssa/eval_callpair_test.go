package ssa

import "testing"

// callPairOp adds an OpCallPair to callee with the given args and returns its
// two results (tag, payload).
func callPairOp(f *Func, b *Block, callee string, args ...Value) (Value, Value) {
	tag, payload := f.AddCallPair(b, args...)
	b.Ops[len(b.Ops)-1].Str = callee
	return tag, payload
}

// split(x) returns the pair (x, x+100) via a TermRetPair; caller sums the two
// results. Exercises OpCallPair + TermRetPair through EvalIn.
func splitFunc() *Func {
	f := NewFunc("split")
	x := f.AddParam()
	e := f.NewBlock()
	hi := f.AddOp(e, OpAdd, x, constIn(f, e, 100))
	f.SetRetPair(e, x, hi)
	return f
}

func TestEvalInCallPair(t *testing.T) {
	split := splitFunc()
	main := NewFunc("main")
	me := main.NewBlock()
	tag, payload := callPairOp(main, me, "split", constIn(main, me, 5))
	main.SetRet(me, main.AddOp(me, OpAdd, tag, payload))

	funcs := map[string]*Func{"split": split, "main": main}
	got, err := EvalIn(funcs, main)
	if err != nil {
		t.Fatalf("EvalIn: %v", err)
	}
	if got != 110 { // 5 + (5+100)
		t.Errorf("EvalIn(main) = %d, want 110", got)
	}
}

// A pair-returning function evaluated directly yields its tag (the first value)
// from EvalIn's single-value contract.
func TestEvalInPairReturnTag(t *testing.T) {
	split := splitFunc()
	funcs := map[string]*Func{"split": split}
	got, err := EvalIn(funcs, split, 7)
	if err != nil {
		t.Fatalf("EvalIn: %v", err)
	}
	if got != 7 { // tag is the first pair element
		t.Errorf("EvalIn(split, 7) tag = %d, want 7", got)
	}
}
