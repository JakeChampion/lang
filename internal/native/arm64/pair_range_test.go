package arm64_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// stp/ldp scale a signed 7-bit immediate by 8, so the reachable offsets are the
// multiples of 8 in [-512, 504]. The encoder masks the field, which turns an
// offset past that into a different, perfectly valid instruction addressing the
// wrong slot — the kind of miscompile nothing downstream can catch. It has to
// be an error.
func TestPairOffsetOutOfRangeIsRejected(t *testing.T) {
	for _, asm := range []string{
		"\tstp x0, x1, [sp, #512]\n",
		"\tldp x0, x1, [sp, #512]\n",
		"\tstp x0, x1, [sp, #-520]\n",
		"\tstp x0, x1, [sp, #-528]!\n",
		"\tldp x0, x1, [sp], #4096\n",
		"\tstp x0, x1, [sp, #12]\n", // not a multiple of 8
	} {
		if _, err := arm64.Assemble(asm); err == nil {
			t.Errorf("%q assembled; want an out-of-range error", strings.TrimSpace(asm))
		}
	}
}

// The boundaries themselves are reachable and must keep assembling.
func TestPairOffsetBoundariesAreAccepted(t *testing.T) {
	for _, asm := range []string{
		"\tstp x0, x1, [sp, #504]\n",
		"\tldp x0, x1, [sp, #504]\n",
		"\tstp x0, x1, [sp, #-512]\n",
		"\tstp x29, x30, [sp, #-16]!\n",
		"\tldp x29, x30, [sp], #16\n",
	} {
		if _, err := arm64.Assemble(asm); err != nil {
			t.Errorf("%q: %v", strings.TrimSpace(asm), err)
		}
	}
}
