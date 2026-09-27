package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A usize op reaches the lift with Width == ir.WidthPtr. On a 64-bit target
// that is an address: the op is 64 bits wide and ResolveWidths carries its
// address-ness on to whatever is derived from it. Dropped to the i32
// default, `base + n` on a usize cast from an i64 — a syscall's return, a
// loaded word — was sign-extended from 32 bits, so a mapping above 2 GiB read
// through the sum faulted while `base` alone still worked.
func TestLiftPointerWidthArithmeticIsAnAddress(t *testing.T) {
	for _, c := range []struct {
		name     string
		ptrW     int
		wantW    int8
		wantAddr bool
	}{
		{"ptrW 8 widens and marks the address", 8, 64, true},
		{"ptrW 4 keeps the i32 default", 4, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := &ir.Func{
				Name: "f",
				PtrW: c.ptrW,
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 4096},
					{Kind: ir.OpConstI32, I32: 65535},
					{Kind: ir.OpAdd, Width: ir.WidthPtr},
					{Kind: ir.OpConstI32, I32: 0},
					{Kind: ir.OpGtS, Width: ir.WidthPtr},
					{Kind: ir.OpReturn},
				},
			}
			out, err := LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			seen := 0
			for _, b := range out.Blocks {
				for _, op := range b.Ops {
					if op.Kind != OpAdd && op.Kind != OpGt {
						continue
					}
					seen++
					if op.Width != c.wantW || op.Addr != c.wantAddr {
						t.Errorf("%v: Width = %d, Addr = %v; want %d, %v", op.Kind, op.Width, op.Addr, c.wantW, c.wantAddr)
					}
				}
			}
			if seen != 2 {
				t.Fatalf("lifted %d arithmetic ops, want the add and the compare", seen)
			}
		})
	}
}
