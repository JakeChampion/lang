package semir

import "github.com/jakechampion/lang/internal/ssa"

type armEnumLayout struct {
	offsets []int64
	size    int64
}

// Layout is target-specific and belongs to this lowering instance, not to the
// semantic interface. Compute each active variant once rather than allocating
// and scanning its entire field list for every individual payload projection.
func (l *armLowerer) enumLayout(e *enumContract, index int) ([]int64, int64) {
	variant := &e.variants[index]
	if layout, ok := l.enums[variant]; ok {
		return layout.offsets, layout.size
	}
	fields := aggregateShape{enum: e.fields[variant.first : variant.first+variant.count]}
	offsets, size := armAggregateLayoutAt(fields, 4)
	if l.enums == nil {
		l.enums = make(map[*enumVariant]armEnumLayout)
	}
	l.enums[variant] = armEnumLayout{offsets, size}
	return offsets, size
}

// Reached only after the runtime proved the enum box has one counted owner.
// Immortal nullary sentinels take dropHelper's non-unique, no-op decrement path.
func (b *armBuilder) dropEnum(value ssa.Value, e *enumContract) {
	tag := b.load(value, 0, armI32)
	for i, variant := range e.variants {
		var next *ssa.Block
		if i != len(e.variants)-1 {
			body := b.f.NewBlock()
			next = b.f.NewBlock()
			condition := b.op(ssa.OpEq, 32, false, tag, b.constant(int64(i)))
			b.f.SetBrIf(b.b, condition, body, next)
			b.b = body
		}
		fields := aggregateShape{enum: e.fields[variant.first : variant.first+variant.count]}
		offsets, size := b.l.enumLayout(e, i)
		for j := range fields.len() {
			if typ := fields.at(j); referenceBearing(typ) {
				b.drop(b.load(value, offsets[j], typ), typ)
			}
		}
		b.call("__fern_box_free", 64, true, value, b.constant(size))
		b.f.SetRet(b.b, value)
		b.b = next
	}
}
