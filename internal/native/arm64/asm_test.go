package arm64_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// TestAssembleExprImmediate checks that a constant `+`/`-` expression in
// an immediate (which GAS folds, and which the backend emits for some
// frame offsets, e.g. `[x29, #96 + 48]`) assembles to the same bytes as
// the pre-folded literal. Toolchain-free: it compares two Assemble runs.
func TestAssembleExprImmediate(t *testing.T) {
	cases := [][2]string{
		{"\tldr x23, [x29, #96 + 48]\n", "\tldr x23, [x29, #144]\n"},
		{"\tadd x0, x1, #8 + 4\n", "\tadd x0, x1, #12\n"},
		{"\tsub x2, x3, #32 - 16\n", "\tsub x2, x3, #16\n"},
		{"\tmov x0, #16 + 16\n", "\tmov x0, #32\n"},
	}
	for _, c := range cases {
		got, err := arm64.Assemble(c[0])
		if err != nil {
			t.Fatalf("Assemble(%q): %v", c[0], err)
		}
		want, err := arm64.Assemble(c[1])
		if err != nil {
			t.Fatalf("Assemble(%q): %v", c[1], err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%q = % x, want (= %q) % x", c[0], got, c[1], want)
		}
	}
}

// TestAssemblerForwardBranch resolves a forward `b` that skips one
// instruction. The GNU assembler encodes the same snippet as
// 0x14000002 (offset = +2 instructions); see the reference in the
// commit message.
func TestAssemblerForwardBranch(t *testing.T) {
	a := arm64.NewAssembler()
	a.B("end")                   // index 0
	a.Emit(arm64.MOVZ(0, 99, 0)) // index 1 (skipped)
	a.Label("end")               // -> index 2
	a.Emit(arm64.RET(30))        // index 2

	got, err := a.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	want = arm64.Put(want, 0x14000002) // b +2
	want = arm64.Put(want, arm64.MOVZ(0, 99, 0))
	want = arm64.Put(want, arm64.RET(30))
	if !bytes.Equal(got, want) {
		t.Fatalf("forward branch:\n got % x\n want % x", got, want)
	}
}

// TestAssemblerBackwardBranch resolves a backward `b` to the previous
// instruction — GNU `as` encodes b-to-(-1) as 0x17ffffff.
func TestAssemblerBackwardBranch(t *testing.T) {
	a := arm64.NewAssembler()
	a.Label("top")              // -> index 0
	a.Emit(arm64.MOVZ(0, 1, 0)) // index 0
	a.B("top")                  // index 1, offset -1

	got, err := a.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	want = arm64.Put(want, arm64.MOVZ(0, 1, 0))
	want = arm64.Put(want, 0x17ffffff) // b -1
	if !bytes.Equal(got, want) {
		t.Fatalf("backward branch:\n got % x\n want % x", got, want)
	}
}

// TestAssemblerCondAndCBZOffsets checks the imm19 placement (bits
// [23:5]) for a conditional branch and a cbz, each skipping one
// instruction (offset +2).
func TestAssemblerCondAndCBZOffsets(t *testing.T) {
	a := arm64.NewAssembler()
	a.Bcond(arm64.CondEQ, "x") // index 0: b.eq +2
	a.Emit(arm64.RET(30))      // index 1
	a.Label("x")               // -> index 2
	a.CBZ(3, "y")              // index 2: cbz x3, +2
	a.Emit(arm64.RET(30))      // index 3
	a.Label("y")               // -> index 4

	got, err := a.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	want = arm64.Put(want, 0x54000000|(2<<5)|arm64.CondEQ) // b.eq +2
	want = arm64.Put(want, arm64.RET(30))
	want = arm64.Put(want, 0xB4000000|(2<<5)|3) // cbz x3, +2
	want = arm64.Put(want, arm64.RET(30))
	if !bytes.Equal(got, want) {
		t.Fatalf("cond/cbz offsets:\n got % x\n want % x", got, want)
	}
}

// TestAssemblerCBNZAndBL covers the remaining branch forms: a cbnz
// (imm19) skipping one instruction and a bl (imm26) to the next.
func TestAssemblerCBNZAndBL(t *testing.T) {
	a := arm64.NewAssembler()
	a.CBNZ(2, "skip")     // index 0: cbnz x2, +2
	a.Emit(arm64.RET(30)) // index 1
	a.Label("skip")       // -> index 2
	a.BL("sub")           // index 2: bl +1
	a.Label("sub")        // -> index 3
	a.Emit(arm64.RET(30)) // index 3

	got, err := a.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	want = arm64.Put(want, 0xB5000000|(2<<5)|2) // cbnz x2, +2
	want = arm64.Put(want, arm64.RET(30))
	want = arm64.Put(want, 0x94000001) // bl +1
	want = arm64.Put(want, arm64.RET(30))
	if !bytes.Equal(got, want) {
		t.Fatalf("cbnz/bl:\n got % x\n want % x", got, want)
	}
}

// TestAssemblerUndefinedLabel surfaces an error rather than silently
// emitting a bogus offset.
func TestAssemblerUndefinedLabel(t *testing.T) {
	a := arm64.NewAssembler()
	a.B("nowhere")
	if _, err := a.Bytes(); err == nil {
		t.Fatal("expected an error for an undefined label")
	}
}
