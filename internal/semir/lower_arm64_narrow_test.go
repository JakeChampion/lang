package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func TestARM64TypedNarrowElementsExecute(t *testing.T) {
	armLauncher(t)
	for _, tc := range []struct {
		name      string
		max, tail int64
		signed    bool
	}{
		{"u8", 255, 128, false}, {"i8", 127, -1, true},
		{"u16", 65535, 32768, false}, {"i16", 32767, -1, true},
	} {
		for _, optimize := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/optimized-%t", tc.name, optimize), func(t *testing.T) {
				var out *ARM64Program
				if tc.name == "u8" {
					out = lowerCheckedARM64(t, fmt.Sprintf(`
function pilot(item: %[1]s): %[1]s[] { return [1%[1]s, %[2]d%[1]s].append(item); }
function get(items: %[1]s[], index: i32): %[1]s { return items[index]; }
function release(own items: %[1]s[]): i32 { return 0; }
`, tc.name, tc.max))
				} else {
					// Only u8 is a source-language narrow integer today. Exercise
					// the remaining complete semantic/layout contracts directly,
					// without adding imaginary type spellings to the frontend.
					width := 16
					if tc.name == "i8" {
						width = 8
					}
					out = narrowARM64Program(t, ast.NumberType{Width: width, Signed: tc.signed}, tc.max)
				}
				b := harnessBuilder(out)
				items := b.call(out.Symbols["pilot"], 64, true, b.constant(tc.tail))
				bad := b.op(ssa.OpNe, 32, false, b.load(items, -4, armU32), b.constant(3))
				for i, want := range []int64{1, tc.max, tc.tail} {
					value := b.call(out.Symbols["get"], 32, false, items, b.constant(int64(i)))
					kind := ssa.OpExtendU
					if tc.signed {
						kind = ssa.OpExtendS
					}
					value = b.op(kind, 64, false, value)
					wrong := b.op(ssa.OpNe, 32, false, value, b.constant(want))
					bad = b.op(ssa.OpOr, 32, false, bad, wrong)
				}
				b.call(out.Symbols["release"], 32, false, items)
				underflow := b.call("__fern_rc_underflow_count", 32, false)
				b.f.SetRet(b.b, b.op(ssa.OpOr, 32, false, bad, underflow))
				requireARM64Success(t, out, b.f.Name, optimize)
			})
		}
	}
}

func narrowARM64Program(t *testing.T, typ ast.NumberType, max int64) *ARM64Program {
	t.Helper()
	pos := ast.Position{}
	arrayType := ast.ArrayType{Elem: typ}
	pilot, get, release := newFunc("pilot", arrayType), newFunc("get", typ), newFunc("release", armI32)
	item := pilot.addParam(typ, ParamValue, pos)
	b := pilot.graph.NewBlock()
	var values []ssa.Value
	for _, n := range []int64{1, max} {
		v := pilot.addOp(b, ssa.OpConstInt, typ, pos)
		b.Ops[len(b.Ops)-1].Imm = n
		values = append(values, v)
	}
	initial := pilot.addOp(b, ssa.OpArrayMake, arrayType, pos, values...)
	result := pilot.addOp(b, ssa.OpArrayAppend, arrayType, pos, initial, item)
	pilot.graph.SetRet(b, result)
	array, index := get.addParam(arrayType, ParamBorrow, pos), get.addParam(armI32, ParamValue, pos)
	b = get.graph.NewBlock()
	get.graph.SetRet(b, get.addOp(b, ssa.OpArrayGet, typ, pos, array, index))
	release.addParam(arrayType, ParamCounted, pos)
	b = release.graph.NewBlock()
	release.graph.SetRet(b, release.addOp(b, ssa.OpConstInt, armI32, pos))
	p := &Program{funcs: []*Func{pilot, get, release}, byName: map[string]int64{"pilot": 1, "get": 2, "release": 3}}
	for _, f := range p.funcs {
		f.program = p
		f.contract = funcContract{result: f.result, modes: append([]ParamMode(nil), f.modes...)}
		for _, param := range f.graph.Params {
			f.contract.params = append(f.contract.params, f.values[param.ID].typ.source)
		}
	}
	out, err := LowerARM64SSA(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
