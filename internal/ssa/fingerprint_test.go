package ssa

import "testing"

// Every field the optimiser can change moves the fingerprint, and a change
// that is undone restores it.
func TestFingerprintTracksEveryField(t *testing.T) {
	build := func() (*Func, *Block, *Block) {
		f := NewFunc("f")
		x := f.AddParam()
		entry := f.NewBlock()
		exit := f.NewBlock()
		c := f.AddOp(entry, OpConstInt)
		entry.Ops[0].Imm = 5
		cmp := f.AddOp(entry, OpLt, x, c)
		f.SetBrIf(entry, cmp, exit, exit)
		s := f.AddOp(exit, OpConstString)
		exit.Ops[0].Str = "s"
		f.AddOp(exit, OpCall, s)
		exit.Ops[1].Str = "callee"
		f.SetRet(exit, x)
		return f, entry, exit
	}
	f, entry, exit := build()
	base := fingerprint(f)
	if g, _, _ := build(); fingerprint(g) != base {
		t.Fatalf("two identical functions fingerprint differently")
	}

	edits := map[string]func(){
		"op kind":       func() { entry.Ops[1].Kind = OpLe },
		"result":        func() { entry.Ops[0].Result.ID += 100 },
		"result2":       func() { entry.Ops[0].Result2.ID = 7 },
		"arg":           func() { entry.Ops[1].Args[0] = entry.Ops[1].Args[1] },
		"arg count":     func() { entry.Ops[1].Args = entry.Ops[1].Args[:1] },
		"imm":           func() { entry.Ops[0].Imm = 6 },
		"f64":           func() { entry.Ops[0].F64 = 1.5 },
		"str":           func() { exit.Ops[1].Str = "other" },
		"width":         func() { entry.Ops[0].Width = 32 },
		"addr":          func() { entry.Ops[0].Addr = true },
		"capture slots": func() { exit.Ops[1].CaptureSlots = []int32{2} },
		"op removed":    func() { exit.Ops = exit.Ops[:1] },
		"block order":   func() { f.Blocks[0], f.Blocks[1] = f.Blocks[1], f.Blocks[0] },
		"block removed": func() { f.Blocks = f.Blocks[:1] },
		"preds":         func() { exit.Preds = nil },
		"term kind":     func() { entry.Term.Kind = TermBr },
		"term cond":     func() { entry.Term.Cond = exit.Ops[0].Result },
		"term true":     func() { entry.Term.True = nil },
		"term false":    func() { entry.Term.False = entry },
		"term target":   func() { entry.Term.Target = exit },
		"term value":    func() { exit.Term.Value = Value{} },
		"term value2":   func() { exit.Term.Value2 = exit.Ops[0].Result },
	}
	for name, edit := range edits {
		f, entry, exit = build()
		edit()
		if fingerprint(f) == base {
			t.Errorf("%s: fingerprint unchanged by the edit", name)
		}
	}
}

// Comparing fingerprints stops the loop where comparing the rendered text did:
// the same number of iterations and the same function at the end.
func TestOptimizeConvergesWhereTextComparisonDid(t *testing.T) {
	for name, build := range optimizeCorpus() {
		t.Run(name, func(t *testing.T) {
			ref := build()
			refIters := 0
			prev := ref.String()
			for i := 1; i <= maxOptimizeIters; i++ {
				runPasses(ref)
				refIters = i
				cur := ref.String()
				if cur == prev {
					break
				}
				prev = cur
			}
			f := build()
			if iters := Optimize(f); iters != refIters {
				t.Errorf("Optimize took %d iterations, text comparison took %d", iters, refIters)
			}
			if got, want := f.String(), ref.String(); got != want {
				t.Errorf("Optimize result differs from the text-comparison loop:\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// optimizeCorpus builds functions that take more than one iteration to settle,
// so the loop's stopping rule is what the test exercises.
func optimizeCorpus() map[string]func() *Func {
	return map[string]func() *Func{
		"const chain": func() *Func {
			f := NewFunc("f")
			entry := f.NewBlock()
			one := f.AddOp(entry, OpConstInt)
			entry.Ops[0].Imm = 1
			two := f.AddOp(entry, OpConstInt)
			entry.Ops[1].Imm = 2
			three := f.AddOp(entry, OpConstInt)
			entry.Ops[2].Imm = 3
			lhs := f.AddOp(entry, OpAdd, one, two)
			rhs := f.AddOp(entry, OpSub, three, one)
			f.SetRet(entry, f.AddOp(entry, OpMul, lhs, rhs))
			return f
		},
		"folded branch": func() *Func {
			f := NewFunc("f")
			x := f.AddParam()
			entry := f.NewBlock()
			thenB := f.NewBlock()
			elseB := f.NewBlock()
			join := f.NewBlock()
			a := f.AddOp(entry, OpConstInt)
			entry.Ops[0].Imm = 5
			b := f.AddOp(entry, OpConstInt)
			entry.Ops[1].Imm = 5
			f.SetBrIf(entry, f.AddOp(entry, OpEq, a, b), thenB, elseB)
			t1 := f.AddOp(thenB, OpAdd, x, a)
			f.SetBr(thenB, join)
			e1 := f.AddOp(elseB, OpSub, x, a)
			f.SetBr(elseB, join)
			phi := f.AddOp(join, OpPhi, t1, e1)
			f.SetRet(join, f.AddOp(join, OpAdd, phi, f.AddOp(join, OpAdd, x, a)))
			return f
		},
		"loop with invariant": func() *Func {
			f := NewFunc("f")
			n := f.AddParam()
			k := f.AddParam()
			entry := f.NewBlock()
			head := f.NewBlock()
			body := f.NewBlock()
			exit := f.NewBlock()
			zero := f.AddOp(entry, OpConstInt)
			entry.Ops[0].Imm = 0
			f.SetBr(entry, head)
			i := f.AddOp(head, OpPhi, zero, Value{})
			acc := f.AddOp(head, OpPhi, zero, Value{})
			f.SetBrIf(head, f.AddOp(head, OpLt, i, n), body, exit)
			inv := f.AddOp(body, OpMul, k, k)
			inv2 := f.AddOp(body, OpMul, k, k)
			acc2 := f.AddOp(body, OpAdd, acc, f.AddOp(body, OpAdd, inv, inv2))
			one := f.AddOp(body, OpConstInt)
			body.Ops[len(body.Ops)-1].Imm = 1
			i2 := f.AddOp(body, OpAdd, i, one)
			f.SetBr(body, head)
			head.Ops[0].Args[1] = i2
			head.Ops[1].Args[1] = acc2
			f.SetRet(exit, acc)
			return f
		},
	}
}
