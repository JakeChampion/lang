package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func harnessBuilder(out *ARM64Program) *armBuilder {
	f := ssa.NewFunc("__semir_test_entry")
	out.Functions[f.Name] = f
	return &armBuilder{f: f, b: f.NewBlock(), l: &armLowerer{out: out}}
}

func requireARM64Success(t *testing.T, out *ARM64Program, entry string, optimize bool) {
	t.Helper()
	stdout, stderr, code := runARM64Pilot(t, armExecutable(t, out, entry, optimize))
	if code != 0 || stdout != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	requireBalancedCensus(t, stderr)
}

func TestARM64TypedEscapedArraySurvivesAllocatorReuse(t *testing.T) {
	armLauncher(t)
	for _, rounds := range []int64{1, 64} {
		for _, optimize := range []bool{false, true} {
			t.Run(fmt.Sprintf("rounds-%d/optimized-%t", rounds, optimize), func(t *testing.T) {
				out := lowerCheckedARM64(t, `
function pilot(): i64[] { var box = [[11i64, 22i64, 33i64]]; return box[0]; }
function release(own items: i64[]): i32 { return 0; }
`)
				b := harnessBuilder(out)
				held := b.call(out.Symbols["pilot"], 64, true)
				b.loop(b.constant(rounds), func(_ ssa.Value) {
					noise := b.array(b.constant(3), 8)
					for i := int64(0); i < 3; i++ {
						b.store(noise, i*8, b.constant(90+i), armU64)
					}
					b.call(out.Symbols["release"], 32, false, noise)
				})
				bad := b.constant(0)
				for i, want := range []int64{11, 22, 33} {
					wrong := b.op(ssa.OpNe, 32, false, b.load(held, int64(i)*8, armU64), b.constant(want))
					bad = b.op(ssa.OpOr, 32, false, bad, wrong)
				}
				wrongRC := b.op(ssa.OpNe, 32, false, b.load(held, -8, armU32), b.constant(1))
				bad = b.op(ssa.OpOr, 32, false, bad, wrongRC)
				b.call(out.Symbols["release"], 32, false, held)
				underflow := b.call("__fern_rc_underflow_count", 32, false)
				b.f.SetRet(b.b, b.op(ssa.OpOr, 32, false, bad, underflow))
				requireARM64Success(t, out, b.f.Name, optimize)
			})
		}
	}
}

func TestARM64TypedAppendPreservesSharedSnapshot(t *testing.T) {
	armLauncher(t)
	out := lowerCheckedARM64(t, `
function pilot(items: i64[]): i64[] { return items.append(99i64); }
function release(own items: i64[]): i32 { return 0; }
`)
	b := harnessBuilder(out)
	old := b.array(b.constant(2), 8)
	b.store(old, 0, b.constant(17), armU64)
	b.store(old, 8, b.constant(29), armU64)
	updated := b.call(out.Symbols["pilot"], 64, true, old)
	bad := b.constant(0)
	for _, check := range []struct {
		ptr          ssa.Value
		offset, want int64
		typ          ast.Type
	}{
		{old, -4, 2, armU32}, {old, 0, 17, armU64}, {old, 8, 29, armU64},
		{updated, -4, 3, armU32}, {updated, 0, 17, armU64}, {updated, 8, 29, armU64}, {updated, 16, 99, armU64},
	} {
		wrong := b.op(ssa.OpNe, 32, false, b.load(check.ptr, check.offset, check.typ), b.constant(check.want))
		bad = b.op(ssa.OpOr, 32, false, bad, wrong)
	}
	b.call(out.Symbols["release"], 32, false, old)
	b.call(out.Symbols["release"], 32, false, updated)
	b.f.SetRet(b.b, bad)
	requireARM64Success(t, out, b.f.Name, true)
}

func TestARM64TypedIndicesCheckBeforeNarrowing(t *testing.T) {
	armLauncher(t)
	for _, typ := range []string{"i64", "u64", "i32", "u32"} {
		for _, index := range []int64{0, -1, 1 << 32} {
			if (typ == "i32" || typ == "u32") && index == 1<<32 {
				continue
			}
			t.Run(fmt.Sprintf("%s/%d", typ, index), func(t *testing.T) {
				var out *ARM64Program
				if typ == "i32" {
					out = lowerCheckedARM64(t, `function pilot(index: i32): i64 { var items = [41i64]; return items[index]; }`)
				} else {
					// The current source checker only admits i32 indexing. The
					// typed operation supports settled integer widths; test that
					// lowering contract directly without weakening the checker.
					width := 64
					if typ == "u32" {
						width = 32
					}
					it := ast.NumberType{Width: width, Signed: typ == "i64"}
					f := newFunc("pilot", ast.NumberType{Width: 64, Signed: true})
					arg := f.addParam(it, ParamValue, ast.Position{})
					block := f.graph.NewBlock()
					value := f.addOp(block, ssa.OpConstInt, f.result, ast.Position{})
					block.Ops[0].Imm = 41
					array := f.addOp(block, ssa.OpArrayMake, ast.ArrayType{Elem: f.result}, ast.Position{}, value)
					result := f.addOp(block, ssa.OpArrayGet, f.result, ast.Position{}, array, arg)
					f.graph.SetRet(block, result)
					out = lowerSemanticARM64(t, f)
				}
				b := harnessBuilder(out)
				value := b.call(out.Symbols["pilot"], 64, false, b.constant(index))
				b.f.SetRet(b.b, value)
				_, stderr, code := runARM64Pilot(t, armExecutable(t, out, b.f.Name, true))
				want := 134
				if index == 0 {
					want = 41
					requireBalancedCensus(t, stderr)
				}
				if code != want {
					t.Fatalf("exit %d, want %d; stderr %q", code, want, stderr)
				}
			})
		}
	}
}

func TestARM64TypedBorrowedStringUnitsBalance(t *testing.T) {
	armLauncher(t)
	out := lowerCheckedARM64(t, `
function pilot(item: string): string {
  var items = [item, item];
  var box = (items.append(item), [item]);
  let (grown, _) = box;
  return grown[2];
}
`)
	b := harnessBuilder(out)
	// Controlled single-word heap string with one independent harness owner.
	// Keep that owner until the compiled function's acquired return is dropped,
	// so the runtime's known final-string-free limitation is not hidden here.
	base := b.op(ssa.OpAlloc, 64, true, b.constant(12))
	b.store(base, 0, b.constant(1), armI32)
	b.store(base, 4, b.constant(3), armI32)
	str := b.offset(base, 8)
	for i, ch := range []byte{'a', 0, 'z', 0} {
		b.store(str, int64(i), b.constant(int64(ch)), ast.NumberType{Width: 8})
	}
	result := b.call(out.Symbols["pilot"], 64, true, str)
	bad := b.op(ssa.OpNe, 32, false, b.load(str, -8, armU32), b.constant(2))
	b.call("__fern_str_dec", 64, true, result)
	wrong := b.op(ssa.OpNe, 32, false, b.load(str, -8, armU32), b.constant(1))
	bad = b.op(ssa.OpOr, 32, false, bad, wrong)
	for i, ch := range []byte{'a', 0, 'z'} {
		wrong = b.op(ssa.OpNe, 32, false, b.load(str, int64(i), ast.NumberType{Width: 8}), b.constant(int64(ch)))
		bad = b.op(ssa.OpOr, 32, false, bad, wrong)
	}
	// This harness knows its exact allocation extent and still owns the last
	// unit, so it can free its own fixture independently of the string runtime.
	b.call("__fern_box_free", 64, true, str, b.constant(4))
	b.f.SetRet(b.b, bad)
	requireARM64Success(t, out, b.f.Name, true)
}

func TestARM64TypedLoopBackEdgeExecutesAndReclaims(t *testing.T) {
	armLauncher(t)
	f := newFunc("pilot", ast.StringType{})
	entry, header, body, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	pos := ast.Position{}
	first := f.addOp(entry, ssa.OpConstString, ast.StringType{}, pos)
	entry.Ops[0].Str = "first"
	arrayType := ast.ArrayType{Elem: ast.StringType{}}
	initial := f.addOp(entry, ssa.OpArrayMake, arrayType, pos, first)
	yes := f.addOp(entry, ssa.OpConstBool, ast.BoolType{}, pos)
	entry.Ops[len(entry.Ops)-1].Imm = 1
	f.graph.SetBr(entry, header)
	f.graph.SetBr(body, header)
	array := f.addPhi(header, arrayType, pos, initial, initial)
	more := f.addPhi(header, ast.BoolType{}, pos, yes, yes)
	f.graph.SetBrIf(header, more, body, exit)
	last := f.addOp(body, ssa.OpConstString, ast.StringType{}, pos)
	body.Ops[0].Str = "last"
	updated := f.addOp(body, ssa.OpArrayAppend, arrayType, pos, array, last)
	no := f.addOp(body, ssa.OpConstBool, ast.BoolType{}, pos)
	header.Ops[0].Args[1], header.Ops[1].Args[1] = updated, no
	index := f.addOp(exit, ssa.OpConstInt, armI32, pos)
	exit.Ops[0].Imm = 1
	result := f.addOp(exit, ssa.OpArrayGet, ast.StringType{}, pos, array, index)
	f.graph.SetRet(exit, result)
	out := lowerSemanticARM64(t, f)
	stdout, stderr, code := runARM64Pilot(t, armExecutable(t, out, printHarness(out), true))
	if stdout != "last\n" || code != 0 {
		t.Fatalf("stdout %q, stderr %q, code %d", stdout, stderr, code)
	}
	requireBalancedCensus(t, stderr)
}

func lowerSemanticARM64(t *testing.T, f *Func) *ARM64Program {
	t.Helper()
	p := &Program{funcs: []*Func{f}, byName: map[string]int64{f.graph.Name: 1}}
	f.program, f.contract = p, funcContract{result: f.result, modes: append([]ParamMode(nil), f.modes...)}
	for _, param := range f.graph.Params {
		f.contract.params = append(f.contract.params, f.values[param.ID].typ)
	}
	out, err := LowerARM64SSA(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
