package ssa

import "testing"

func TestTrivialPhiAliasesFollowOperandChains(t *testing.T) {
	f := NewFunc("aliases")
	payload := f.AddParam()
	entry, mid, end := f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBr(entry, mid)
	first := f.AddPhi(mid, payload)
	f.SetBr(mid, end)
	second := f.AddPhi(end, first)
	f.SetRet(end, second)
	aliases := make(ValueAliases)
	TrivialPhisWithAliases(f, aliases)
	for _, value := range []Value{first, second, payload} {
		if got := aliases.Resolve(value); got != end.Term.Value || got != payload {
			t.Fatalf("metadata alias %v became %v, operand became %v", value, got, end.Term.Value)
		}
	}
	DCE(f)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if aliases.Resolve(second) != payload {
		t.Fatal("discarding phi instructions changed the returned aliases")
	}
}

func TestTrivialPhiAliasesPreserveMaterializedIdentity(t *testing.T) {
	f := NewFunc("constants")
	flag := f.AddParam()
	entry, yes, no, join := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(entry, flag, yes, no)
	one := f.AddOp(yes, OpConstInt)
	two := f.AddOp(no, OpConstInt)
	f.SetBr(yes, join)
	f.SetBr(no, join)
	phi := f.AddPhi(join, one, two)
	f.SetRet(join, phi)
	aliases := make(ValueAliases)
	TrivialPhisWithAliases(f, aliases)
	if aliases.Resolve(phi) != phi || join.Ops[0].Kind != OpConstInt {
		t.Fatal("materialized constant was incorrectly aliased to a predecessor")
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
}

func TestTrivialPhiAliasesNilInput(t *testing.T) {
	aliases := ValueAliases{1: Value{ID: 2}}
	TrivialPhisWithAliases(nil, aliases)
	if len(aliases) != 0 || aliases.Resolve(Value{}) != (Value{}) {
		t.Fatal("nil input must clear stale aliases")
	}
}
