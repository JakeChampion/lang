package arm64_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// #8139: two immediate spellings GNU as accepts that this assembler refused.
// It stands in for gas as the oracle for the self-host differentials, so a
// form it cannot encode is a form those differentials cannot check — the gap
// reads as agreement, because both sides are absent.
//
//   - imm12 is unsigned, so gas spells a negative add/sub immediate as the
//     opposite mnemonic with the magnitude: `add #-16` is `sub #16`, `adds`
//     becomes `subs`, `cmp` becomes `cmn`.
//   - a 32-bit logical immediate takes the low half of the value, so `-16`
//     is the mask 0xFFFFFFF0 rather than an out-of-range 64-bit value.
//
// Every expectation comes from gas itself rather than a table, so the two
// cannot drift apart the way a pinned constant would.

func negImmLines() []string {
	var out []string
	for _, mnem := range []string{"add", "sub", "adds", "subs"} {
		for _, reg := range []string{"x1, x2", "w1, w2"} {
			// -16773120 is 0xFFF000, the largest magnitude the shifted
			// form reaches; one step past it is in the refusal list.
			for _, v := range []string{"-1", "-16", "-4095", "-4096", "-16773120"} {
				out = append(out, fmt.Sprintf("%s %s, #%s", mnem, reg, v))
			}
		}
	}
	// sp is the operand that made the positive case matter (#3598); the
	// negative spelling reaches the same encoder.
	out = append(out, "add sp, sp, #-16", "sub sp, sp, #-16",
		"add sp, sp, #-4096", "sub sp, sp, #-4096")
	for _, mnem := range []string{"cmp", "cmn"} {
		for _, reg := range []string{"x1", "w1"} {
			for _, v := range []string{"-1", "-16", "-4095", "-4096"} {
				out = append(out, fmt.Sprintf("%s %s, #%s", mnem, reg, v))
			}
		}
	}
	// The explicit-shift spelling takes the rewrite too.
	out = append(out, "add x1, x2, #-1, lsl #12", "sub x1, x2, #-1, lsl #12",
		"adds x1, x2, #-2, lsl #12", "subs w1, w2, #-2, lsl #12")
	return out
}

func logicalWidthLines() []string {
	var out []string
	// The 32-bit form is not the 64-bit one with sf cleared: it needs N=0
	// and the pattern replicated across both halves, so these encode from
	// the low 32 bits regardless of how the value was written.
	for _, mnem := range []string{"and", "orr", "eor", "ands"} {
		for _, v := range []string{"-16", "-2", "0xfffffff0", "0xfffffffe",
			"0xf0f0f0f0", "0xffffffff80000000", "0xffffffff0000ffff", "0xff"} {
			out = append(out, fmt.Sprintf("%s w1, w2, #%s", mnem, v))
		}
	}
	out = append(out, "tst w1, #-16", "tst w1, #0xfffffff0", "tst w1, #-2")
	// The 64-bit siblings, so a change to the width split cannot quietly
	// move them.
	for _, mnem := range []string{"and", "orr", "eor", "ands"} {
		for _, v := range []string{"-16", "-2", "0xfffffff0",
			"0x5555555555555555", "0xfffffffffffffffe"} {
			out = append(out, fmt.Sprintf("%s x1, x2, #%s", mnem, v))
		}
	}
	return out
}

func assertMatchesGas(t *testing.T, lines []string) {
	t.Helper()
	as, objcopy := findBinutils(t)
	for _, line := range lines {
		src := "\t" + line + "\n"
		got, err := arm64.Assemble(src)
		if err != nil {
			t.Errorf("%s: Assemble: %v", line, err)
			continue
		}
		want := gnuAsText(t, as, objcopy, ".text\n"+src)
		if !bytes.Equal(got, want) {
			t.Errorf("%s: got % x, want % x", line, got, want)
		}
	}
}

func TestNegativeAddSubImmediateMatchesGNUAs(t *testing.T) {
	lines := negImmLines()
	if len(lines) < 50 {
		t.Fatalf("only %d lines; the matrix is meant to be a product of mnemonics, widths and magnitudes", len(lines))
	}
	assertMatchesGas(t, lines)
}

func TestLogicalImmediateWidthMatchesGNUAs(t *testing.T) {
	assertMatchesGas(t, logicalWidthLines())
}

// TestImmediateRefusalsMatchGNUAs pins the other half: widening the accepted
// set must not make the assembler accept what gas rejects, or the oracle
// starts vouching for lines no assembler can produce.
func TestImmediateRefusalsMatchGNUAs(t *testing.T) {
	for _, line := range []string{
		// Magnitude outside imm12's two forms, so no rewrite reaches it.
		"add x1, x2, #-4097",
		"sub w1, w2, #-16777216",
		"cmp x1, #-4097",
		"cmn x1, #-16777215",
		// A shift form whose immediate does not fit the 12-bit field.
		"add x1, x2, #4096, lsl #12",
		"add x1, x2, #-4096, lsl #12",
		// A high half that is neither all-zero nor all-one names a value a
		// w register cannot hold.
		"and w1, w2, #0x1fffffff0",
		"orr w1, w2, #0x100000000",
		// All-ones and zero are not bitmask immediates at either width.
		"and w1, w2, #-1",
		"orr w1, w2, #-4294967296",
		"and x1, x2, #-1",
	} {
		if _, err := arm64.Assemble("\t" + line + "\n"); err == nil {
			t.Errorf("%s: assembled, but GNU as rejects it", line)
		} else if !strings.Contains(err.Error(), "range") && !strings.Contains(err.Error(), "bitmask") {
			t.Errorf("%s: refused with an unrelated error: %v", line, err)
		}
	}
}

// TestMoviBitPatternSpellingsMatchGNUAs pins the movi 64-bit bytemask form
// against gas in both spellings of the same pattern. The operand is a bit
// pattern, and gas takes either the unsigned hex or the signed decimal that
// names it — `#0xff00ff00ff00ff00` and `#-71777214294589696` are one
// instruction. The assembler read this operand with ParseUint, which
// refuses a leading sign, so half of what gas accepts was rejected.
//
// Both the .2d vector form and the scalar D form go through the same
// bytemask path, so both are pinned.
func TestMoviBitPatternSpellingsMatchGNUAs(t *testing.T) {
	assertMatchesGas(t, []string{
		"movi v5.2d, #0xff00ff00ff00ff00",
		"movi v5.2d, #-71777214294589696",
		"movi v0.2d, #0xffffffffffffffff",
		"movi v0.2d, #-1",
		"movi v1.2d, #0xffff0000ffff0000",
		"movi v1.2d, #-281470681808896",
		"movi d6, #0xffffffffffffffff",
		"movi d6, #-1",
		"movi d7, #0xff",
		"movi d8, #0",
	})
}

// TestMoviRejectsNonBytemask holds the other direction: widening the
// accepted spellings must not widen the accepted VALUES. Every byte of the
// 64-bit form has to be 0x00 or 0xff, whichever spelling names it.
func TestMoviRejectsNonBytemask(t *testing.T) {
	for _, line := range []string{
		"movi v0.2d, #0x0102030405060708",
		"movi v0.2d, #-2",
		"movi d0, #0x1234",
	} {
		if _, err := arm64.Assemble("\t" + line + "\n"); err == nil {
			t.Errorf("%s assembled; want a refusal (not a bytemask)", line)
		}
	}
}
