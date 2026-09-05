package arm64ssa

// Length-ceiling guards (#8457): a string byte count and an array element
// count live in a 4-byte signed prefix, so the helpers that build one sum in
// 64 bits and refuse a total the prefix cannot hold. Not reachable without a
// ~2 GiB operand, so asserted on the emitted text.

import (
	"fmt"
	"strings"
	"testing"
)

func wantHelperLines(t *testing.T, what, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s is missing %q:\n%s", what, w, body)
		}
	}
}

func TestSSAStrConcatChecksLengthCeiling(t *testing.T) {
	body := helperText(t, emitStrConcatHelper)
	wantHelperLines(t, "__str_concat", body,
		"add x4, x2, x3",
		"lsr x5, x4, #31",
		"cbnz x5, .Lssa_strcat_len_overflow",
		".Lssa_strcat_len_overflow:")
	if !strings.Contains(body, fmt.Sprintf("mov x0, #%d", lenOverflowExit)) {
		t.Errorf("__str_concat's overflow abort does not exit %d:\n%s", lenOverflowExit, body)
	}
	if !strings.Contains(body, fmt.Sprintf("mov x2, #%d", len(msgAllocSizeOutOfRange))) {
		t.Error("__str_concat's overflow abort writes the wrong diagnostic length")
	}
}

func TestSSAAllocU8RejectsNegativeLength(t *testing.T) {
	body := helperText(t, emitAllocU8Helper)
	wantHelperLines(t, "__alloc_u8", body,
		"tbnz w0, #31, .Lssa_allocu8_len_overflow",
		".Lssa_allocu8_len_overflow:")
}
