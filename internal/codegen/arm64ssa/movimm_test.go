package arm64ssa

import (
	"fmt"
	"strings"
	"testing"
)

// AArch64 `mov` materialises an immediate in one instruction only when it is a
// lone shifted halfword (MOVZ) or the inverse of one (MOVN). Anything else has
// to be spelled movz + movk. gas refuses the rest outright — "immediate cannot
// be moved by a single instruction" — and the in-process assembler took the low
// half instead, so `mov x0, #100000` in int_to_string mis-numbered every value
// at or above 100000 rather than failing.
func TestMovImmLinesSplitsWhatMovCannotEncode(t *testing.T) {
	cases := []struct {
		name  string
		v     uint64
		lines int
	}{
		{"zero", 0, 1},
		{"small", 251, 1},
		{"halfword max", 0xffff, 1},
		{"one shifted halfword", 0xffff << 16, 1},
		{"minus one is MOVN", ^uint64(0), 1},
		{"100000 needs a movk", 100000, 2},
		{"two halfwords", 0x1234_5678, 2},
		// Sign-extended i32 MIN: neither it nor its inverse is one halfword, so
		// it costs the full sequence. This is the value the corpus hits.
		{"sign-extended i32 min", uint64(0xffffffff80000000), 4},
		{"all four halfwords", 0x1234_5678_9abc_def0, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := movImmLines("x0", tc.v)
			if len(got) != tc.lines {
				t.Errorf("movImmLines(%#x) emitted %d line(s), want %d:\n%s",
					tc.v, len(got), tc.lines, strings.Join(got, "\n"))
			}
			// A multi-line sequence must open with the `mov` that seeds the
			// register and continue only in `movk`, or a high halfword would
			// land on stale bits.
			if len(got) > 1 {
				if !strings.HasPrefix(strings.TrimSpace(got[0]), "mov x0,") {
					t.Errorf("first line does not seed with mov: %q", got[0])
				}
				for _, l := range got[1:] {
					if !strings.HasPrefix(strings.TrimSpace(l), "movk ") {
						t.Errorf("continuation is not a movk: %q", l)
					}
				}
			}
		})
	}
}

// The property the sequence actually has to satisfy: it reconstructs the value
// exactly. Re-assemble the halfwords the lines name and compare.
func TestMovImmLinesReconstructsTheValue(t *testing.T) {
	for _, v := range []uint64{
		0, 1, 251, 100000, 0xffff, 0x1_0000, 0xdead_beef,
		^uint64(0), uint64(0xffffffff80000000), 0x1234_5678_9abc_def0,
	} {
		lines := movImmLines("x0", v)
		var got uint64
		for i, l := range lines {
			l = strings.TrimSpace(l)
			if i == 0 {
				var imm int64
				if _, err := fmt.Sscanf(l, "mov x0, #%d", &imm); err != nil {
					t.Fatalf("parsing %q: %v", l, err)
				}
				got = uint64(imm)
				continue
			}
			var imm uint64
			var shift uint
			if _, err := fmt.Sscanf(l, "movk x0, #%d, lsl #%d", &imm, &shift); err != nil {
				t.Fatalf("parsing %q: %v", l, err)
			}
			got |= imm << shift
		}
		if got != v {
			t.Errorf("movImmLines(%#x) reconstructs to %#x:\n%s", v, got, strings.Join(lines, "\n"))
		}
	}
}
