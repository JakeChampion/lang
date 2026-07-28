package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"

	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// The float bit-reinterpret family (f64_bits / f64_from_bits / f32_bits /
// f32_from_bits) had no arm64 rendering: fConvSeq fell through to
// "float conversion %v not supported yet", so `-target arm64-ssa` rejected any
// program reaching it — std/float's to_string does, via __float_sig_pack's
// `f64_bits(n)`, so every f64 `.to_string()` was a hard compile error.
//
// This asserts at the EMITTER, which is host-independent and therefore runs in
// CI. The sibling run-tests below verify the values but skip without
// qemu-aarch64, and the e2e case that would have caught this
// (TestArm64SSACliRoundtrip/f64_to_string_frac) skips for the same reason — the
// aarch64 CI lane runs natively and has no qemu installed, so nothing in CI
// executes it.

// setWidth stamps the width on the op just added (the reinterprets are
// width-sensitive: the 32-bit pair narrows through f32).
func setWidth(b *ssa.Block, w int8) {
	b.Ops[len(b.Ops)-1].Width = w
}

// emitReinterpret builds a one-op function around kind and returns its asm.
func emitReinterpret(t *testing.T, kind ssa.OpKind, w int8) string {
	t.Helper()
	f := ssa.NewFunc("r")
	e := f.NewBlock()
	c := constFloat(f, e, 1.5)
	setWidth(e, w)
	v := f.AddOp(e, kind, c)
	setWidth(e, w)
	f.SetRet(e, v)
	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"r": f}, "r", 1, nil)
	if err != nil {
		t.Fatalf("%v: EmitAsmModule: %v", kind, err)
	}
	return asm
}

// TestReinterpretEmits pins that each of the four ops renders at all. Before
// the fix every one of these failed the emit outright.
func TestReinterpretEmits(t *testing.T) {
	for _, tc := range []struct {
		kind ssa.OpKind
		w    int8
		// want is an instruction the sequence must contain. The 64-bit pair is
		// an identity (a float is already HELD as its f64 bit pattern in a
		// general register, which is also why OpFPromote is a no-op), so there
		// is nothing distinctive to look for beyond a successful emit; the
		// 32-bit pair must narrow/widen through f32.
		want string
	}{
		{ssa.OpReinterpretF64ToI64, 64, ""},
		{ssa.OpReinterpretI64ToF64, 64, ""},
		{ssa.OpReinterpretF32ToI32, 32, "fcvt s0, d0"},
		{ssa.OpReinterpretI32ToF32, 32, "fcvt d0, s0"},
	} {
		asm := emitReinterpret(t, tc.kind, tc.w)
		if tc.want != "" && !strings.Contains(asm, tc.want) {
			t.Errorf("%v: emitted asm does not contain %q\n--- asm ---\n%s", tc.kind, tc.want, asm)
		}
	}
}

// TestReinterpretF64RoundTrip: f64 -> i64 bits -> f64 must return the original
// value. 3.0 is 0x4008000000000000, so the top byte is 0x40 = 64.
func TestReinterpretF64RoundTrip(t *testing.T) {
	f := ssa.NewFunc("r")
	e := f.NewBlock()
	c := constFloat(f, e, 3.0)
	setWidth(e, 64)
	bits := f.AddOp(e, ssa.OpReinterpretF64ToI64, c)
	setWidth(e, 64)
	back := f.AddOp(e, ssa.OpReinterpretI64ToF64, bits)
	setWidth(e, 64)
	again := f.AddOp(e, ssa.OpReinterpretF64ToI64, back)
	setWidth(e, 64)
	// Top byte of the f64 pattern, so the exit code stays in range.
	sh := f.AddOp(e, ssa.OpShrU, again, constOp(f, e, 56))
	setWidth(e, 64)
	f.SetRet(e, sh)

	if got := assembleRunArmModule(t, map[string]*ssa.Func{"r": f}, "r", 1); got != 0x40 {
		t.Errorf("f64 bit round-trip = %d, want %d (0x4008000000000000 >> 56)", got, 0x40)
	}
}

// TestReinterpretF32RoundTrip: an i32 bit pattern read as an f32 and back must
// be unchanged. 0x40200000 is f32 2.5; its low byte is 0.
func TestReinterpretF32RoundTrip(t *testing.T) {
	f := ssa.NewFunc("r")
	e := f.NewBlock()
	raw := constOp(f, e, 0x40200000)
	setWidth(e, 32)
	asF := f.AddOp(e, ssa.OpReinterpretI32ToF32, raw)
	setWidth(e, 32)
	back := f.AddOp(e, ssa.OpReinterpretF32ToI32, asF)
	setWidth(e, 32)
	// Byte 2 of the pattern (0x20 = 32) — distinguishes a real round-trip from
	// a zeroed or truncated result.
	sh := f.AddOp(e, ssa.OpShrU, back, constOp(f, e, 16))
	setWidth(e, 32)
	and := f.AddOp(e, ssa.OpAnd, sh, constOp(f, e, 0xff))
	setWidth(e, 32)
	f.SetRet(e, and)

	if got := assembleRunArmModule(t, map[string]*ssa.Func{"r": f}, "r", 1); got != 0x20 {
		t.Errorf("f32 bit round-trip = %d, want %d (0x40200000 >> 16 & 0xff)", got, 0x20)
	}
}

// TestWideMemRoundTrip: a full-word (8-byte) load must survive the trip from
// the legacy IR through the lift and out of the backend without being narrowed
// back to i32. memLoadSeq renders `ldr x` and then applies maskFix(dst, in.W),
// which sign-extends from bit 31 unless the width is 64 — so when the lift set
// the 8-byte KIND but left Width 0, every wide value read out of memory lost
// its top half. That corrupted i64[] elements (std/float's bignum limbs among
// them): 2576980379 came back as -1717986917, 1234567890123 as its own low 32
// bits, 1912276171.
//
// Built from ir.Func and lifted here rather than hand-assembled as ssa.Func,
// because the defect was the LIFT dropping the width — a test that stamps
// Width 64 on the SSA op directly passes either way and guards nothing.
//
// The values cover the three ways the stray sxtw showed up: bit 31 set
// (becomes negative), all-ones in the low word (becomes -1), and a value wider
// than 32 bits (truncated outright).
func TestWideMemRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		val   int64
		shift int64
		want  int
	}{
		// Every case reads a byte at or above bit 32 — exactly the half the
		// stray sxtw destroyed. The first two are 32-bit values whose high
		// word must read back as ZERO; sign-extension turns it into 0xff.
		{"bit31 set", 2576980379, 32, 0x00},         // 0x9999999B -> 0xff when sxtw'd
		{"low word all ones", 4294967295, 32, 0x00}, // 0xFFFFFFFF -> 0xff when sxtw'd
		{"wider than 32 bits", 1234567890123, 32, 0x1F},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// buf = alloc(16); buf[0] = val; return (buf[0] >> shift) & 0xff
			in := &ir.Func{
				Name: "r",
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 16},
					{Kind: ir.OpAlloc},
					{Kind: ir.OpStoreLocal, I32: 0},

					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpConstI64, I64: tc.val, Width: 64},
					{Kind: ir.OpStore, Width: 64},

					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpLoad, Width: 64},
					{Kind: ir.OpConstI64, I64: tc.shift, Width: 64},
					{Kind: ir.OpShrS, Width: 64},
					{Kind: ir.OpConstI64, I64: 0xff, Width: 64},
					{Kind: ir.OpAnd, Width: 64},
					{Kind: ir.OpReturn},
				},
				Locals: []*ast.Var{{Name: "buf"}},
			}
			f, err := ssa.LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			if got := assembleRunArmModule(t, map[string]*ssa.Func{"r": f}, "r", 1); got != tc.want {
				t.Errorf("(%d >> %d) & 0xff = %d, want %d — the wide value was narrowed on the way out of memory", tc.val, tc.shift, got, tc.want)
			}
		})
	}
}
