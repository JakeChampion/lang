package arm64ssa

import (
	"strings"
	"testing"
)

// The pair forms scale a signed 7-bit offset by 8, so [sp, #504] is the last
// slot one can reach. A deeper slot has to go out as a single str/ldr: the
// assembler rejects an out-of-range pair, and an unchecked encoder would have
// silently truncated the offset to a different, valid one.
func TestSlotLinesStopPairingPastTheOffsetLimit(t *testing.T) {
	regs := []int{0, 1, 2, 3}
	// Slot 62 puts the first pair at #496 and the second at #512.
	got := slotSaveLines(regs, 62)
	want := []string{
		"stp x0, x1, [sp, #496]",
		"str x2, [sp, #512]",
		"str x3, [sp, #520]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An odd register count leaves one slot over; it goes out on its own rather
// than pairing with whatever follows the area.
func TestSlotLinesEmitTheOddTailAlone(t *testing.T) {
	got := slotRestoreLines([]int{0, 1, 2}, 0)
	want := []string{
		"ldp x0, x1, [sp, #0]",
		"ldr x2, [sp, #16]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
