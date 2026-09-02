package arm64_test

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// AL and NV (#8075). GNU as does not treat them as simply allowed or simply
// banned — it splits by mnemonic class, and neither assembler implemented the
// split:
//
//   - allowed on the forms that use the condition directly (b.cond, csel and
//     friends, ccmp, fcsel, fccmp);
//   - refused on the aliases that INVERT it (cset, csetm, cinc, cinv, cneg),
//     with "operand 2 must be one of the standard conditions, excluding AL
//     and NV", because inverting "always" or "never" names no instruction.
//
// Every `want` is what aarch64-linux-gnu-as emitted for the same line, read
// back with objdump. NV matters most: it is a DISTINCT encoding (0b1111), not
// a spelling of AL, so folding the two would round-trip a program into a
// different instruction.
func TestCondAlNvMatchesGas(t *testing.T) {
	for _, c := range []struct {
		asm  string
		want uint32
	}{
		{"l0:\n\tb.al l0", 0x5400000e},
		{"l0:\n\tb.nv l0", 0x5400000f},
		{"\tcsel x0, x1, x2, al", 0x9a82e020},
		{"\tcsel x0, x1, x2, nv", 0x9a82f020},
		{"\tcsinc x0, x1, x2, al", 0x9a82e420},
		{"\tcsinv x0, x1, x2, nv", 0xda82f020},
		{"\tccmp x0, x1, #0, al", 0xfa41e000},
		{"\tccmp x0, x1, #0, nv", 0xfa41f000},
		{"\tccmn x0, x1, #0, al", 0xba41e000},
		{"\tfcsel d0, d1, d2, al", 0x1e62ec20},
		{"\tfccmp d0, d1, #0, al", 0x1e61e400},
	} {
		text, _, err := arm64.AssembleProgram(".text\n_start:\n"+c.asm+"\n", 0x400000)
		if err != nil {
			t.Errorf("%q: GNU as assembles this and we reject it: %v", c.asm, err)
			continue
		}
		if got := binary.LittleEndian.Uint32(text[len(text)-4:]); got != c.want {
			t.Errorf("%q: got %08x, GNU as emits %08x", c.asm, got, c.want)
		}
	}
}

// TestCondAlNvRefusedOnInvertingAliases is the other half. These five encode
// the inverse of the written condition, so accepting AL or NV would emit the
// opposite of what was asked for — the shape that made this worth finding.
func TestCondAlNvRefusedOnInvertingAliases(t *testing.T) {
	for _, bad := range []string{
		"cset x0, al", "cset x0, nv",
		"csetm x0, al", "csetm x0, nv",
		"cinc x0, x1, al", "cinv x0, x1, nv", "cneg x0, x1, al",
	} {
		_, _, err := arm64.AssembleProgram(".text\n_start:\n\t"+bad+"\n", 0x400000)
		if err == nil {
			t.Errorf("%q: accepted, but GNU as refuses it", bad)
			continue
		}
		// The diagnostic has to say why, or the next reader assumes the
		// condition is unknown and "fixes" it by adding AL to the table.
		if !strings.Contains(err.Error(), "inverse") {
			t.Errorf("%q: rejected, but the reason does not mention the inversion: %v", bad, err)
		}
	}
}

// TestEveryConditionSpellingAssembles keeps the table honest in the direction
// the AL gap sat in: a spelling missing from condCodes is not a compile error
// anywhere, it is just an instruction the assembler cannot take.
func TestEveryConditionSpellingAssembles(t *testing.T) {
	for _, cond := range []string{
		"eq", "ne", "cs", "hs", "cc", "lo", "mi", "pl",
		"vs", "vc", "hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
	} {
		for _, form := range []string{
			"l0:\n\tb." + cond + " l0",
			"\tcsel x0, x1, x2, " + cond,
			"\tccmp x0, x1, #0, " + cond,
		} {
			if _, _, err := arm64.AssembleProgram(".text\n_start:\n"+form+"\n", 0x400000); err != nil {
				t.Errorf("%q: %v", form, err)
			}
		}
	}
}
