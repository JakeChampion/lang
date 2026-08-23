package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A call to a backend-provided builtin or runtime helper is the one call whose
// result width nothing downstream can derive: there is no ssa.Func to read a
// signature off, and the name alone says nothing. internal/ir stamps the
// classification onto the op at the site that mints the call, and the lift is
// where it becomes the SSA's own Width / Addr.
//
// Getting it wrong is silent both ways. A helper returning a heap pointer that
// reads as narrow has its result sign-extended from 32 bits, so every address
// above 0x7fffffff comes back negative — and both arenas are based at
// 0x4_0000_0000, so that is every address. A helper returning an f64 masked the
// same way keeps the low mantissa half and loses the sign and exponent.
func TestLiftCallResultWidthFromTheIRStamp(t *testing.T) {
	cases := []struct {
		name      string
		stamp     int
		wantWidth int8
		wantAddr  bool
	}{
		{"narrow result keeps the i32 mask", ir.ResNarrow, 0, false},
		{"wide result drops the mask", ir.ResWide, 64, false},
		{"address result drops the mask and propagates", ir.ResAddr, 64, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := &ir.Func{
				Name: "f",
				Ops: []ir.Op{
					{Kind: ir.OpCallDirect, Runtime: true, Str: "__some_helper", I32: 0, Width: c.stamp},
					{Kind: ir.OpReturn},
				},
			}
			out, err := LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			call := findCall(out, "__some_helper")
			if call == nil {
				t.Fatal("no OpCall in the lifted function")
			}
			if call.Width != c.wantWidth || call.Addr != c.wantAddr {
				t.Errorf("Width = %d, Addr = %v; want %d, %v",
					call.Width, call.Addr, c.wantWidth, c.wantAddr)
			}
		})
	}
}

// The dedicated rc kinds carry no stamp because each names exactly one helper:
// __fern_rc_inc and __fern_rc_dec hand back the pointer they were given, and
// __fern_rc_is_unique answers with a boolean.
func TestLiftRcOpResultWidth(t *testing.T) {
	cases := []struct {
		kind      ir.OpKind
		callee    string
		wantWidth int8
		wantAddr  bool
	}{
		{ir.OpRcInc, "__fern_rc_inc", 64, true},
		{ir.OpRcDec, "__fern_rc_dec", 64, true},
		{ir.OpRcIsUnique, "__fern_rc_is_unique", 0, false},
	}
	for _, c := range cases {
		t.Run(c.callee, func(t *testing.T) {
			in := &ir.Func{
				Name: "f",
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 0},
					{Kind: c.kind, Str: c.callee, I32: 1},
					{Kind: ir.OpReturn},
				},
			}
			out, err := LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			call := findCall(out, c.callee)
			if call == nil {
				t.Fatalf("no OpCall to %s in the lifted function", c.callee)
			}
			if call.Width != c.wantWidth || call.Addr != c.wantAddr {
				t.Errorf("Width = %d, Addr = %v; want %d, %v",
					call.Width, call.Addr, c.wantWidth, c.wantAddr)
			}
		})
	}
}

func findCall(f *Func, callee string) *Op {
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Kind == OpCall && op.Str == callee {
				return op
			}
		}
	}
	return nil
}
