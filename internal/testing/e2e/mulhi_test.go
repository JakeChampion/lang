package e2e

// `__mulhi_u64(a, b)` is the high 64 bits of the unsigned 128-bit product: one
// `umulh` on arm64, `mulq` on x86-64, and four 32x32 partial products on wasm,
// which has no such instruction. Go's bits.Mul64 is the oracle.

import (
	"fmt"
	"math/bits"
	"strings"
	"testing"
)

func mulhiProg() string {
	values := []uint64{0, 1, 2, 3, 0xffffffff, 0x100000000, 0xffffffffffffffff, 0x8000000000000000,
		0xaaaaaaaaaaaaaaaa, 0x5555555555555555, 0x0123456789abcdef, 0xfedcba9876543210, 0xa2f9836e4e441529}
	var src strings.Builder
	src.WriteString("@noinline function hi(a: u64, b: u64): u64 { return __mulhi_u64(a, b); }\n")
	src.WriteString("function main(): i32 {\n  let n: i32 = 0;\n")
	k := 0
	for _, a := range values {
		for _, b := range values {
			want, _ := bits.Mul64(a, b)
			k++
			fmt.Fprintf(&src, "  if (hi(%du64, %du64) != %du64) { return %d; }\n", a, b, want, k%100+1)
		}
	}
	src.WriteString("  return 0;\n}\n")
	return src.String()
}

func TestMulhiInterp(t *testing.T) {
	if got := runInterpExit(t, mulhiProg()); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestMulhiX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mulhiProg()); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestMulhiWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mulhiProg()); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestMulhiArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mulhiProg()); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
