package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

const maxArmAllocation int64 = 1<<31 - 1

var armI32 = ast.NumberType{Width: 32, Signed: true}
var armU32 = ast.NumberType{Width: 32}
var armU64 = ast.NumberType{Width: 64}

// These layouts are explicit for the existing single-word ARM64 SSA ABI.
// Never consult ast.TwoWordOverride to reconstruct a target after lowering.
func armElementBytes(typ ast.Type) int64 {
	if n, ok := typ.(ast.NumberType); ok {
		if n.Width == ast.WidthPtr || n.NormalWidth() == 64 {
			return 8
		}
		if n.NormalWidth() == 8 {
			return 1
		}
		return 4
	}
	return 8
}

func armTupleLayout(typ ast.TupleType) ([]int64, int64) {
	var size int64
	offsets := make([]int64, len(typ.Elems))
	for i, elem := range typ.Elems {
		width := int64(4)
		if referenceBearing(elem) {
			width = 8
		}
		if n, ok := elem.(ast.NumberType); ok && (n.Width == ast.WidthPtr || n.NormalWidth() == 64) {
			width = 8
		}
		size = (size + width - 1) & -width
		offsets[i] = size
		size += width
	}
	return offsets, size
}

func (l *armLowerer) helper(key string, params []ast.Type, result ast.Type) (*armBuilder, string, bool) {
	if name := l.helpers[key]; name != "" {
		return nil, name, false
	}
	shapes := make([]armABIValue, len(params))
	for i, typ := range params {
		shapes[i].width, shapes[i].addr = armValueShape(typ)
	}
	w, addr := armValueShape(result)
	return l.physicalHelper(key, shapes, armABIValue{w, addr})
}

// Runtime-only pointer arithmetic has physical shapes, not invented source
// types. In particular an indexing helper returns an address, not a string.
type armABIValue struct {
	width int8
	addr  bool
}

func (l *armLowerer) physicalHelper(key string, params []armABIValue, result armABIValue) (*armBuilder, string, bool) {
	if name := l.helpers[key]; name != "" {
		return nil, name, false
	}
	index := len(l.out.Functions)
	name := fmt.Sprintf("__semir_helper_%d", index)
	for l.out.Functions[name] != nil {
		index++
		name = fmt.Sprintf("__semir_helper_%d", index)
	}
	f := ssa.NewFunc(name)
	f.ReturnWidth, f.ReturnAddr = result.width, result.addr
	for _, shape := range params {
		f.ParamWidths = append(f.ParamWidths, shape.width)
		f.ParamAddrs = append(f.ParamAddrs, shape.addr)
		f.AddParam()
	}
	l.helpers[key], l.out.Functions[name] = name, f
	return &armBuilder{f: f, b: f.NewBlock(), l: l}, name, true
}

func (b *armBuilder) array(length ssa.Value, stride int64) ssa.Value {
	bytes := b.op(ssa.OpMul, 64, false, length, b.constant(stride))
	bytes = b.op(ssa.OpAdd, 64, false, bytes, b.constant(16))
	base := b.op(ssa.OpAlloc, 64, true, bytes)
	b.store(base, 4, length, armI32)
	b.store(base, 8, b.constant(1), armI32)
	b.store(base, 12, length, armI32)
	return b.offset(base, 16)
}

func (b *armBuilder) drop(value ssa.Value, typ ast.Type) {
	if !referenceBearing(typ) {
		return
	}
	name := "__fern_str_dec"
	if _, stringType := typ.(ast.StringType); !stringType {
		name = b.l.dropHelper(typ)
	}
	b.call(name, 64, true, value)
}

func (l *armLowerer) dropHelper(typ ast.Type) string {
	b, name, fresh := l.helper("drop:"+typ.String(), []ast.Type{typ}, typ)
	if !fresh {
		return name
	}
	value := b.f.Params[0]
	yes, no := b.f.NewBlock(), b.f.NewBlock()
	unique := b.call("__fern_rc_is_unique", 32, false, value)
	b.f.SetBrIf(b.b, unique, yes, no)
	b.b = no
	b.call("__fern_rc_dec", 64, true, value)
	b.f.SetRet(b.b, value)
	b.b = yes
	switch t := typ.(type) {
	case ast.ArrayType:
		stride := armElementBytes(t.Elem)
		if referenceBearing(t.Elem) {
			length := b.load(value, -4, armU32)
			b.loop(length, func(index ssa.Value) {
				at := b.op(ssa.OpAdd, 64, true, value, b.op(ssa.OpMul, 64, false, index, b.constant(stride)))
				b.drop(b.load(at, 0, t.Elem), t.Elem)
			})
		}
		b.call("__fern_arr_dec", 64, true, value, b.constant(stride))
	case ast.TupleType:
		offsets, size := armTupleLayout(t)
		for i, elem := range t.Elems {
			if referenceBearing(elem) {
				b.drop(b.load(value, offsets[i], elem), elem)
			}
		}
		b.call("__fern_box_free", 64, true, value, b.constant(size))
	}
	b.f.SetRet(b.b, value)
	return name
}

// loop generates a full-width induction variable and returns with b at exit.
// The body may itself split blocks; its final block supplies the back edge.
func (b *armBuilder) loop(length ssa.Value, body func(ssa.Value)) {
	zero := b.constant(0)
	header, work, exit := b.f.NewBlock(), b.f.NewBlock(), b.f.NewBlock()
	b.f.SetBr(b.b, header)
	index := b.f.AddPhi(header, zero, zero)
	header.Ops[0].Width = 64
	b.b = header
	cond := b.op(ssa.OpLtU, 32, false, index, length)
	b.f.SetBrIf(header, cond, work, exit)
	b.b = work
	body(index)
	next := b.op(ssa.OpAdd, 64, false, index, b.constant(1))
	b.f.SetBr(b.b, header)
	header.Ops[0].Args[1] = next
	b.b = exit
}

func (b *armBuilder) require(condition ssa.Value, message string) {
	ok, fail := b.f.NewBlock(), b.f.NewBlock()
	b.f.SetBrIf(b.b, condition, ok, fail)
	b.b = fail
	if message != "" {
		str := b.op(ssa.OpConstString, 64, true)
		b.b.Ops[len(b.b.Ops)-1].Str = message
		b.call("eprint", 32, false, str)
	}
	b.call("exit", 32, false, b.constant(134))
	// The existing runtime exit never returns. A syntactic return keeps
	// the existing low-level CFG well formed without inventing a trap op.
	b.f.SetRet(b.b, b.constant(0))
	b.b = ok
}

func (l *armLowerer) indexHelper(stride int64) string {
	b, name, fresh := l.physicalHelper(fmt.Sprintf("index:%d", stride), []armABIValue{{64, true}, {64, false}}, armABIValue{64, true})
	if !fresh {
		return name
	}
	array, index := b.f.Params[0], b.f.Params[1]
	length := b.load(array, -4, armU32)
	b.require(b.op(ssa.OpLtU, 32, false, index, length), "")
	address := b.op(ssa.OpAdd, 64, true, array, b.op(ssa.OpMul, 64, false, index, b.constant(stride)))
	b.f.SetRet(b.b, address)
	return name
}

func (l *armLowerer) appendHelper(elem ast.Type) string {
	arrayType := ast.ArrayType{Elem: elem}
	b, name, fresh := l.helper("append:"+elem.String(), []ast.Type{arrayType, elem}, arrayType)
	if !fresh {
		return name
	}
	array, item := b.f.Params[0], b.f.Params[1]
	stride := armElementBytes(elem)
	length := b.load(array, -4, armU32)
	// The source length is zero-extended before arithmetic. Check the total
	// byte extent against the allocator's signed-i32 size ABI before narrowing.
	limit := (maxArmAllocation - 16) / stride
	b.require(b.op(ssa.OpLtU, 32, false, length, b.constant(limit)), "fern: allocation size out of range\n")
	newLength := b.op(ssa.OpAdd, 64, false, length, b.constant(1))
	result := b.array(newLength, stride)
	if referenceBearing(elem) {
		b.loop(length, func(index ssa.Value) {
			offset := b.op(ssa.OpMul, 64, false, index, b.constant(stride))
			from := b.op(ssa.OpAdd, 64, true, array, offset)
			to := b.op(ssa.OpAdd, 64, true, result, offset)
			child := b.load(from, 0, elem)
			b.call("__fern_rc_inc", 64, true, child)
			b.store(to, 0, child, elem)
		})
	} else {
		bytes := b.op(ssa.OpMul, 64, false, length, b.constant(stride))
		b.call("__memcpy", 64, true, result, array, bytes)
	}
	tail := b.op(ssa.OpAdd, 64, true, result, b.op(ssa.OpMul, 64, false, length, b.constant(stride)))
	// The caller's verified storeValue supply has already acquired or moved
	// exactly one unit for item, independently of each copied child above.
	// In particular, append(array, array[index]) retains the projected child
	// before this call; the borrowed original array stays live through copying.
	// This store consumes that supplied unit. Retaining it again here leaks.
	b.store(tail, 0, item, elem)
	b.f.SetRet(b.b, result)
	return name
}
