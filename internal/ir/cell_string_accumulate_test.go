package ir

import (
	"testing"
)

// `c.set(c.get() + piece)` is a cell accumulating, and it was miscompiled
// two ways at once (#8067).
//
// The overwrite released the slot's buffer BEFORE the value expression ran,
// so the `c.get()` inside that expression read a buffer already at rc 0 —
// silent loss on x86-64, where the freelist then handed the address out
// again, and a segfault once it was reused. And the concat lowered to the
// in-place `__fern_str_append`, which CONSUMES its left operand: a cell read
// is owned only in the sense of carrying a retain on top of the reference
// the slot still holds, so growing it in place mutated a live value and left
// the two references disagreeing about who releases it.
//
// Both halves are checked here at the op level, because the runtime symptom
// is backend- and allocator-dependent: the same wrong IR printed the right
// answer on arm64 and wasm.
func TestLowerCellStringAccumulateOrder(t *testing.T) {
	const src = `function build(): i32 {
    var c: Cell[string] = cell_new("");
    c.set(c.get() + "one;");
    return c.get().len();
}`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		ops := funcOpsOf(p, "build")
		dec, concat, app := -1, -1, -1
		for i, op := range ops {
			switch {
			case isNamedCallKind(op.Kind) && op.Str == "__fern_str_dec" && dec < 0:
				dec = i
			case isNamedCallKind(op.Kind) && op.Str == "__fern_str_append" && app < 0:
				app = i
			case op.Kind == OpStrConcat && concat < 0:
				concat = i
			}
		}
		read := app
		if read < 0 {
			read = concat
		}
		if dec < 0 || read < 0 {
			t.Fatalf("ptrW=%d: expected both a release and a concat in the body:\n%s", ptrW, p)
		}
		if dec < read {
			t.Errorf("ptrW=%d: the overwrite releases the old buffer at op %d, before the value expression reads it at op %d — that is #8067:\n%s",
				ptrW, dec, read, p)
		}

		// The concat must not take the consuming in-place append: the left
		// operand is a cell read, whose buffer the slot still owns.
		if app >= 0 {
			t.Errorf("ptrW=%d: a cell read must not be consumed by the in-place __fern_str_append:\n%s", ptrW, p)
		}
	}
}

// funcOpsOf returns the named function's op stream, or nil.
func funcOpsOf(p *Program, name string) []Op {
	for _, fn := range p.Funcs {
		if fn.Name == name {
			return fn.Ops
		}
	}
	return nil
}
