package x86_64ssa

// Length-ceiling guards (#8457): a string byte count and an array element
// count live in a 4-byte signed prefix, so the helpers that build one sum in
// 64 bits and refuse a total the prefix cannot hold. Not reachable without a
// ~2 GiB operand, so asserted on the emitted text.

import (
	"fmt"
	"strings"
	"testing"
)

// helperText runs one helper emitter and returns its assembly text.
func helperText(emit func(func(string, ...any))) string {
	var b strings.Builder
	emit(func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	})
	return b.String()
}

func wantHelperLines(t *testing.T, what, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s is missing %q:\n%s", what, w, body)
		}
	}
}

func TestSSAStrConcatChecksLengthCeiling(t *testing.T) {
	body := helperText(emitStrConcatHelper)
	wantHelperLines(t, "__str_concat", body,
		"lea r8, [rcx + rdx]",
		"cmp r8, 2147483647",
		"ja .Lssa_strcat_len_overflow",
		".Lssa_strcat_len_overflow:")
	if !strings.Contains(body, fmt.Sprintf("mov edi, %d", lenOverflowExit)) {
		t.Errorf("__str_concat's overflow abort does not exit %d:\n%s", lenOverflowExit, body)
	}
	if !strings.Contains(body, fmt.Sprintf("mov edx, %d", len(msgAllocSizeOutOfRange))) {
		t.Error("__str_concat's overflow abort writes the wrong diagnostic length")
	}
}

func TestSSAAllocU8RejectsNegativeLength(t *testing.T) {
	body := helperText(emitAllocU8Helper)
	wantHelperLines(t, "__alloc_u8", body,
		"test edi, edi",
		"js .Lssa_allocu8_len_overflow",
		"lea rdx, [rsi + 16]",
		".Lssa_allocu8_len_overflow:")
}
