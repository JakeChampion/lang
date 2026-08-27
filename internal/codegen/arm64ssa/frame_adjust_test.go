package arm64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// A frame past 4095 bytes cannot be opened with one instruction: the ADD/SUB
// immediate is 12 bits with an optional 12-bit left shift, so it takes the
// 4096-multiple part and the remainder. Emitting the single form anyway is what
// the assembler rejected on a self-host module with 20 KB of spill slots.
func TestLargeFrameOpensInTwoInstructions(t *testing.T) {
	for _, c := range []struct {
		bytes int
		want  []string
	}{
		{16, []string{"sub sp, sp, #16"}},
		{4080, []string{"sub sp, sp, #4080"}},
		{4096, []string{"sub sp, sp, #4096"}},
		{20304, []string{"sub sp, sp, #16384", "sub sp, sp, #3920"}},
	} {
		got, err := spAdjustLines("sub", c.bytes)
		if err != nil {
			t.Fatalf("%d bytes: %v", c.bytes, err)
		}
		if strings.Join(got, "; ") != strings.Join(c.want, "; ") {
			t.Errorf("%d bytes -> %q, want %q", c.bytes, got, c.want)
		}
	}
}

// The contract is the assembler's, so ask it: every frame size in range has to
// assemble, opened and closed.
func TestEveryFrameSizeAssembles(t *testing.T) {
	step := 16
	for n := 16; n <= maxFrameBytes; n += step {
		var src strings.Builder
		for _, op := range []string{"sub", "add"} {
			lines, err := spAdjustLines(op, n)
			if err != nil {
				t.Fatalf("%d bytes: %v", n, err)
			}
			for _, l := range lines {
				src.WriteString("\t" + l + "\n")
			}
		}
		if _, err := arm64.Assemble(src.String()); err != nil {
			t.Fatalf("%d bytes: %v\n%s", n, err, src.String())
		}
		if n > 1<<16 {
			step = 4096 + 16 // thin the sweep out once the shifted half is in play
		}
	}
}

// Beyond what two immediates reach it is an error, never a silently wrong
// frame.
func TestFrameBeyondTwoImmediatesIsRejected(t *testing.T) {
	if _, err := spAdjustLines("sub", maxFrameBytes+16); err == nil {
		t.Error("a frame past the two-immediate reach was accepted")
	}
}
