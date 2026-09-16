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
		".Lssa_strcat_len_overflow:",
		"jmp "+abortAllocSize)
}

func TestSSAAllocU8RejectsNegativeLength(t *testing.T) {
	body := helperText(emitAllocU8Helper)
	wantHelperLines(t, "__alloc_u8", body,
		"test edi, edi",
		"js .Lssa_allocu8_len_overflow",
		"lea edi, [rbx + 16]",
		".Lssa_allocu8_len_overflow:")
}
