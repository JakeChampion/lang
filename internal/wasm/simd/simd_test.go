package simd_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/simd"
)

// vecCase pairs one instruction's WAT spelling with the bytes this package
// produces for it. `wat` is a complete function body, `enc` builds the same
// body with the encoders — including the surrounding local.get / v128.load /
// drop, so the byte sequence being matched is long enough that a coincidental
// hit inside the module's other sections is not a real possibility.
type vecCase struct {
	name string
	wat  string
	enc  func([]byte) []byte
	// params/result of the enclosing function, defaulting to (param i32).
	params string
}

// loadV128 pushes a v128 read from the address in local 0, at the natural
// alignment wasm-tools defaults to when the text omits `align=`.
func loadV128(b []byte) []byte {
	b = inst.InstLocalGet(b, 0)
	return simd.InstV128Load(b, 4, 0)
}

const watLoad = "local.get 0\nv128.load\n"

func unary(op func([]byte) []byte) func([]byte) []byte {
	return func(b []byte) []byte {
		b = loadV128(b)
		b = op(b)
		return inst.InstDrop(b)
	}
}

func binary(op func([]byte) []byte) func([]byte) []byte {
	return func(b []byte) []byte {
		b = loadV128(b)
		b = loadV128(b)
		b = op(b)
		return inst.InstDrop(b)
	}
}

func vecCases() []vecCase {
	u := func(name string, op func([]byte) []byte) vecCase {
		return vecCase{name: name, wat: watLoad + name + "\ndrop\n", enc: unary(op)}
	}
	b := func(name string, op func([]byte) []byte) vecCase {
		return vecCase{name: name, wat: watLoad + watLoad + name + "\ndrop\n", enc: binary(op)}
	}
	cases := []vecCase{
		{
			name: "v128.load",
			wat:  watLoad + "drop\n",
			enc:  func(b []byte) []byte { return inst.InstDrop(loadV128(b)) },
		},
		{
			name: "v128.store",
			wat:  "local.get 0\n" + watLoad + "v128.store\n",
			enc: func(b []byte) []byte {
				b = inst.InstLocalGet(b, 0)
				b = loadV128(b)
				return simd.InstV128Store(b, 4, 0)
			},
		},
		{
			name: "i8x16.splat",
			wat:  "local.get 0\ni8x16.splat\ndrop\n",
			enc: func(b []byte) []byte {
				return inst.InstDrop(simd.InstI8x16Splat(inst.InstLocalGet(b, 0)))
			},
		},
		{
			name: "i16x8.splat",
			wat:  "local.get 0\ni16x8.splat\ndrop\n",
			enc: func(b []byte) []byte {
				return inst.InstDrop(simd.InstI16x8Splat(inst.InstLocalGet(b, 0)))
			},
		},
		{
			name: "i32x4.splat",
			wat:  "local.get 0\ni32x4.splat\ndrop\n",
			enc: func(b []byte) []byte {
				return inst.InstDrop(simd.InstI32x4Splat(inst.InstLocalGet(b, 0)))
			},
		},
		{
			name: "i64x2.splat",
			wat:  "i64.const 0\ni64x2.splat\ndrop\n",
			enc: func(b []byte) []byte {
				return inst.InstDrop(simd.InstI64x2Splat(inst.InstI64Const(b, 0)))
			},
		},
	}
	cases = append(cases,
		b("i8x16.eq", simd.InstI8x16Eq),
		b("i8x16.ne", simd.InstI8x16Ne),
		b("i8x16.lt_u", simd.InstI8x16LtU),
		b("i8x16.gt_u", simd.InstI8x16GtU),
		b("i8x16.le_u", simd.InstI8x16LeU),
		b("i8x16.ge_u", simd.InstI8x16GeU),
		b("i16x8.eq", simd.InstI16x8Eq),
		b("i16x8.ne", simd.InstI16x8Ne),
		b("i32x4.eq", simd.InstI32x4Eq),
		b("i32x4.ne", simd.InstI32x4Ne),
		b("i64x2.eq", simd.InstI64x2Eq),
		b("i64x2.ne", simd.InstI64x2Ne),
		b("v128.and", simd.InstV128And),
		b("v128.andnot", simd.InstV128AndNot),
		b("v128.or", simd.InstV128Or),
		b("v128.xor", simd.InstV128Xor),
		u("v128.not", simd.InstV128Not),
		u("v128.any_true", simd.InstV128AnyTrue),
		u("i8x16.all_true", simd.InstI8x16AllTrue),
		u("i8x16.bitmask", simd.InstI8x16Bitmask),
		u("i16x8.all_true", simd.InstI16x8AllTrue),
		u("i16x8.bitmask", simd.InstI16x8Bitmask),
		u("i32x4.all_true", simd.InstI32x4AllTrue),
		u("i32x4.bitmask", simd.InstI32x4Bitmask),
		u("i64x2.all_true", simd.InstI64x2AllTrue),
		u("i64x2.bitmask", simd.InstI64x2Bitmask),
	)
	return cases
}

// TestVectorOpcodes pins each encoding byte-for-byte. Hand-written tables are
// exactly what the wasm-tools differential below exists to distrust, so this
// test is the CHEAP half: it runs everywhere, catches a typo introduced later,
// and does not claim to have verified the opcode numbers against the spec.
// TestVectorOpcodesAgainstWasmTools is what does that.
func TestVectorOpcodes(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"v128.load", simd.InstV128Load(nil, 4, 0), []byte{0xfd, 0x00, 0x04, 0x00}},
		{"v128.load offset", simd.InstV128Load(nil, 0, 16), []byte{0xfd, 0x00, 0x00, 0x10}},
		{"v128.store", simd.InstV128Store(nil, 4, 0), []byte{0xfd, 0x0b, 0x04, 0x00}},
		{"i8x16.splat", simd.InstI8x16Splat(nil), []byte{0xfd, 0x0f}},
		{"i16x8.splat", simd.InstI16x8Splat(nil), []byte{0xfd, 0x10}},
		{"i32x4.splat", simd.InstI32x4Splat(nil), []byte{0xfd, 0x11}},
		{"i64x2.splat", simd.InstI64x2Splat(nil), []byte{0xfd, 0x12}},
		{"i8x16.eq", simd.InstI8x16Eq(nil), []byte{0xfd, 0x23}},
		{"i8x16.ne", simd.InstI8x16Ne(nil), []byte{0xfd, 0x24}},
		{"i8x16.lt_u", simd.InstI8x16LtU(nil), []byte{0xfd, 0x26}},
		{"i8x16.gt_u", simd.InstI8x16GtU(nil), []byte{0xfd, 0x28}},
		{"i8x16.le_u", simd.InstI8x16LeU(nil), []byte{0xfd, 0x2a}},
		{"i8x16.ge_u", simd.InstI8x16GeU(nil), []byte{0xfd, 0x2c}},
		{"i16x8.eq", simd.InstI16x8Eq(nil), []byte{0xfd, 0x2d}},
		{"i32x4.eq", simd.InstI32x4Eq(nil), []byte{0xfd, 0x37}},
		{"v128.not", simd.InstV128Not(nil), []byte{0xfd, 0x4d}},
		{"v128.and", simd.InstV128And(nil), []byte{0xfd, 0x4e}},
		{"v128.andnot", simd.InstV128AndNot(nil), []byte{0xfd, 0x4f}},
		{"v128.or", simd.InstV128Or(nil), []byte{0xfd, 0x50}},
		{"v128.xor", simd.InstV128Xor(nil), []byte{0xfd, 0x51}},
		{"v128.any_true", simd.InstV128AnyTrue(nil), []byte{0xfd, 0x53}},
		{"i8x16.all_true", simd.InstI8x16AllTrue(nil), []byte{0xfd, 0x63}},
		{"i8x16.bitmask", simd.InstI8x16Bitmask(nil), []byte{0xfd, 0x64}},

		// THE ULEB BOUNDARY. Sub-opcode 127 would be one byte and 128 is
		// two; every entry below is on the far side of it. Emitting the
		// sub-opcode as a raw byte instead of a uleb would turn
		// i16x8.bitmask (132) into 0xfd 0x84 — which is not invalid, it is
		// f32x4.pmin. Silent, so it is pinned on both sides.
		{"i16x8.all_true", simd.InstI16x8AllTrue(nil), []byte{0xfd, 0x83, 0x01}},
		{"i16x8.bitmask", simd.InstI16x8Bitmask(nil), []byte{0xfd, 0x84, 0x01}},
		{"i32x4.all_true", simd.InstI32x4AllTrue(nil), []byte{0xfd, 0xa3, 0x01}},
		{"i32x4.bitmask", simd.InstI32x4Bitmask(nil), []byte{0xfd, 0xa4, 0x01}},
		{"i64x2.all_true", simd.InstI64x2AllTrue(nil), []byte{0xfd, 0xc3, 0x01}},
		{"i64x2.bitmask", simd.InstI64x2Bitmask(nil), []byte{0xfd, 0xc4, 0x01}},

		// i64x2.eq/ne are NOT at the 55+10 stride the i8x16 → i16x8 → i32x4
		// series would predict; they were added later and sit at 214/215.
		// Extrapolating gives 0xfd 0x41, a valid f32x4.eq — again silent.
		{"i64x2.eq", simd.InstI64x2Eq(nil), []byte{0xfd, 0xd6, 0x01}},
		{"i64x2.ne", simd.InstI64x2Ne(nil), []byte{0xfd, 0xd7, 0x01}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got % x, want % x", c.name, c.got, c.want)
		}
	}
}

// TestVectorOpcodesAgainstWasmTools is the differential: for each instruction,
// wasm-tools assembles a module containing it and this test asserts the bytes
// this package produces appear verbatim in that module.
//
// This is the wasm analogue of the GNU-`as` gate on the native assemblers. It
// exists because the failure mode of a vector opcode table is not a crash —
// the sub-opcode space is dense, so a wrong number is another valid
// instruction, and the module still validates and still runs. Only an external
// assembler can say which instruction was actually meant.
func TestVectorOpcodesAgainstWasmTools(t *testing.T) {
	wasmTools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	for i, c := range vecCases() {
		params := c.params
		if params == "" {
			params = "(param i32)"
		}
		wat := "(module (memory 1) (func " + params + "\n" + c.wat + "))\n"
		watPath := filepath.Join(dir, "c.wat")
		binPath := filepath.Join(dir, "c.wasm")
		if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		out, err := exec.Command(wasmTools, "parse", watPath, "-o", binPath).CombinedOutput()
		if err != nil {
			t.Fatalf("case %d (%s): wasm-tools parse: %v\n%s\nwat:\n%s", i, c.name, err, out, wat)
		}
		mod, err := os.ReadFile(binPath)
		if err != nil {
			t.Fatalf("read wasm: %v", err)
		}
		want := c.enc(nil)
		if !bytes.Contains(mod, want) {
			t.Errorf("%s: encoder produced % x, which does not appear in the module "+
				"wasm-tools assembled from:\n%s\nmodule bytes: % x", c.name, want, wat, mod)
		}
	}
}

// TestVectorOpcodesRejectedByWasmTools is the counter-test: it confirms the
// containment check above can actually FAIL, by feeding it an encoding that is
// wrong in the one way the table is most likely to be wrong — a sub-opcode
// written as a raw byte rather than a uleb.
//
// Without this, a bug that made every `enc` return an empty slice would leave
// the differential passing on every case (an empty slice is contained in
// everything), which is precisely the shape of silent green this project has
// been bitten by.
func TestVectorOpcodesRejectedByWasmTools(t *testing.T) {
	wasmTools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	watPath := filepath.Join(dir, "c.wat")
	binPath := filepath.Join(dir, "c.wasm")
	wat := "(module (memory 1) (func (param i32)\n" + watLoad + "i16x8.bitmask\ndrop\n))\n"
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	if out, err := exec.Command(wasmTools, "parse", watPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	mod, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}
	// The truncated form: 0xfd 0x84 with the uleb continuation dropped.
	truncated := []byte{simd.Prefix, 0x84}
	full := simd.InstI16x8Bitmask(nil)
	if !bytes.Contains(mod, full) {
		t.Fatalf("i16x8.bitmask: correct encoding % x absent — the differential is broken, "+
			"not just this case", full)
	}
	if bytes.Contains(mod, append(truncated, inst.InstDrop(nil)...)) {
		t.Errorf("the raw-byte form % x followed by drop appears in the module, so the "+
			"containment check cannot distinguish it from the uleb form", truncated)
	}
}

// TestVectorMemargShape asserts the vector load/store memarg is encoded in the
// same (align, offset) uleb pair shape as the scalar loads next door, so the
// two families cannot drift.
func TestVectorMemargShape(t *testing.T) {
	scalar := memory.InstI32Load(nil, 2, 300)
	vector := simd.InstV128Load(nil, 2, 300)
	if !bytes.Equal(scalar[1:], vector[2:]) {
		t.Errorf("memarg tails differ: scalar % x, vector % x", scalar[1:], vector[2:])
	}
}
