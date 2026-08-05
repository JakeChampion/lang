package arm64

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// Three of the four forms in this file were rejected by AssembleProgram until
// #6075, while the SELF-HOST arm64 assembler already supported them. That
// asymmetry mattered because internal/native/arm64 is the oracle
// TestSelfHostArm64AsmEncodingMatchesNative checks the self-host assembler
// against: a form the oracle cannot assemble cannot be covered by the
// differential, so those rows had to be commented out of its snippet — leaving
// the self-host assembler's most recently added encoders (#6060's movn and FP
// stur/ldur) as exactly the code with no differential coverage. Restoring them
// caught a live bug on the first run: `stur d1, [x2, #-16]` was assembling as
// the INTEGER stur of x1.
//
// Numeric local labels unlock the most: they appear in ordinary emitter output
// (every bounds check is `b.lo 1f … 1:`), so until AssembleProgram accepted
// them the differential could only ever run on a hand-written snippet, never on
// a whole compiled program (#6062).
//
// The FOURTH — dot-prefixed local labels — is here as a regression guard rather
// than a new capability: #6075 listed it as a gap and it was not one. See
// TestAssembleDotLocalLabel.
//
// Every expectation here is GNU as's own output for the same source
// (aarch64-linux-gnu-as + objdump), not a hand-derived encoding.

// asmWords assembles src and returns its .text as hex words.
func asmWords(t *testing.T, src string) []string {
	t.Helper()
	text, _, err := AssembleProgram(src, 0x400000)
	if err != nil {
		t.Fatalf("AssembleProgram rejected the source: %v\n%s", err, src)
	}
	var got []string
	for i := 0; i+4 <= len(text); i += 4 {
		got = append(got, fmt.Sprintf("%08x", binary.LittleEndian.Uint32(text[i:])))
	}
	return got
}

func checkWords(t *testing.T, src, wantSpaced string) {
	t.Helper()
	want := strings.Fields(wantSpaced)
	got := asmWords(t, src)
	if len(got) != len(want) {
		t.Fatalf("word count: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("word %d: got %s, want %s (GNU as)", i, got[i], want[i])
		}
	}
}

// TestAssembleNumericLocalLabels pins GAS numeric local labels: `1:` defined
// repeatedly, with `1f` / `1b` naming the NEXT / PREVIOUS definition. The two
// `b.lo 1f` here must reach DIFFERENT targets, and `b 1b` must reach the most
// recent `1:` rather than the first — the property a plain name→offset map
// cannot express, since every definition would overwrite the last.
func TestAssembleNumericLocalLabels(t *testing.T) {
	const src = `.text
_start:
    cmp x1, x2
    b.lo 1f
    b Labort
1:
    add x1, x1, #1
    cmp x1, x2
    b.lo 1f
    b Labort
1:
    add x1, x1, #2
2:
    sub x1, x1, #1
    cbnz x1, 2b
    b 1b
Labort:
    ret
`
	checkWords(t, src, `eb02003f 54000043 14000009 91000421
	                    eb02003f 54000043 14000005 91000821
	                    d1000421 b5ffffe1 17fffffd d65f03c0`)
}

// TestAssembleNumericLocalLabelBackwardUndefined: a `1b` with no preceding
// `1:` must be an error. Resolving it to definition 0 would aim the branch at
// a label defined LATER in the file — a silently wrong target, which is the
// failure mode this whole area has been prone to.
func TestAssembleNumericLocalLabelBackwardUndefined(t *testing.T) {
	const src = `.text
_start:
    b 1b
1:
    ret
`
	if _, _, err := AssembleProgram(src, 0x400000); err == nil {
		t.Fatal("expected an error for `1b` with no preceding `1:`, got none")
	} else if !strings.Contains(err.Error(), "undefined label") {
		t.Errorf("error should name the undefined label, got: %v", err)
	}
}

// TestAssembleMovn covers move-wide-not, including the lsl form and the 32-bit
// W variant. The MOVN encoder existed all along; only the mnemonic was missing
// from the dispatch, so `movn` came back as "unsupported instruction".
func TestAssembleMovn(t *testing.T) {
	const src = `.text
_start:
    movn x0, #99
    movn x3, #1, lsl #16
    movn w5, #7
    ret
`
	checkWords(t, src, "92800c60 92a00023 128000e5 d65f03c0")
}

// TestAssembleFPUnscaled covers the FP unscaled (LDUR/STUR) addressing mode.
// A negative FP displacement used to be rejected outright ("must be a
// non-negative multiple of 8"). Both spellings are checked: the explicit
// stur/ldur mnemonics, and the str/ldr spelling that GNU as itself rewrites to
// them — accepting the latter is matching the reference assembler, not being
// lenient. The final row is the scaled positive case, to prove the unscaled
// path did not capture offsets that belong to the unsigned form.
func TestAssembleFPUnscaled(t *testing.T) {
	const src = `.text
_start:
    str d0, [x12, #-8]
    ldr d0, [x12, #-8]
    stur d1, [x2, #-16]
    ldur d3, [x4, #-32]
    str d0, [x12, #8]
    ret
`
	checkWords(t, src, "fc1f8180 fc5f8180 fc1f0041 fc5e0083 fd000580 d65f03c0")
}

// TestAssembleDollarInSymbol covers '$' in a symbol name. GAS allows it
// (verified: aarch64-linux-gnu-as assembles `foo$wrap0:` and a branch to it),
// and the self-host emitter names every lifted-lambda wrapper that way —
// `__fn_sort__sort_strings_asc_ci$wrap0`. isIdent rejected it, so this
// assembler could not read ANY whole program the emitter produces, which is
// what #6062's alignment needs. Found by trying exactly that.
func TestAssembleDollarInSymbol(t *testing.T) {
	const src = `.text
_start:
    b foo$wrap0
    nop
foo$wrap0:
    ret
`
	checkWords(t, src, "14000002 d503201f d65f03c0")
}

// TestAssembleDotLocalLabel is a REGRESSION GUARD, not a new capability: #6075
// listed dot-prefixed local labels as a third gap, and they already worked —
// splitLabel peels labels before the `.`-prefix directive check, in both
// AssembleProgram and Assemble. The claim appeared in the issue and in the
// differential's own comment, so it is worth a test that would catch anyone
// "fixing" it by reordering those two steps.
func TestAssembleDotLocalLabel(t *testing.T) {
	const src = `.text
_start:
    b .Ldone
    nop
.Ldone:
    ret
`
	checkWords(t, src, "14000002 d503201f d65f03c0")
}
