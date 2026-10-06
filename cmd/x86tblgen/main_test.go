package main

import (
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/x86tbl"
)

const x86NativeFern = "../../compiler/x86_native.fern"

// TestGeneratedFernIsUpToDate is the gate that makes the shared table real. It
// is not enough to generate the Fern side once: if someone edits either the
// table or the generated block by hand, the two assemblers drift again and
// nothing else notices until a mnemonic silently stops assembling (#8071).
func TestGeneratedFernIsUpToDate(t *testing.T) {
	src, err := os.ReadFile(x86NativeFern)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Rewrite(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if out != string(src) {
		t.Errorf("%s is out of date; regenerate with:\n\tgo run ./cmd/x86tblgen %s", x86NativeFern, x86NativeFern)
	}
}

// TestMarkersArePresent keeps the test above from passing vacuously. Rewrite
// leaves a file carrying no markers untouched, so a renamed or deleted marker
// would make the staleness check compare a file against itself and always
// agree.
func TestMarkersArePresent(t *testing.T) {
	src, err := os.ReadFile(x86NativeFern)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if !strings.Contains(string(src), b.begin) {
			t.Errorf("%s carries no %q — Rewrite would leave it alone and the staleness check would pass on any content", x86NativeFern, b.begin)
		}
		if !strings.Contains(string(src), b.end) {
			t.Errorf("%s carries no %q", x86NativeFern, b.end)
		}
	}
}

// TestEverySpellingIsDistinct guards the table's own shape: a duplicated
// spelling would silently give one of its two codes, and a duplicate is easy
// to introduce when adding aliases by hand.
func TestEverySpellingIsDistinct(t *testing.T) {
	seen := map[string]byte{}
	for _, c := range x86tbl.Conds {
		for _, s := range c.Spellings {
			if prev, dup := seen[s]; dup {
				t.Errorf("spelling %q appears under code %d and code %d", s, prev, c.Code)
			}
			seen[s] = c.Code
		}
	}
	if len(seen) != 28 {
		t.Errorf("the table lists %d spellings, want the 28 GNU as accepts", len(seen))
	}
}

// TestSSEHalvesPartitionTheTable pins the split the generated dispatch
// depends on. x86_gas_emit consults the float half before the integer one, so
// a row in both halves would be shadowed and a row in neither would vanish
// from the self-host without the Go side noticing.
func TestSSEHalvesPartitionTheTable(t *testing.T) {
	seen := map[string]x86tbl.SSEHalf{}
	for _, o := range x86tbl.SSEOps {
		if prev, dup := seen[o.Mnemonic]; dup {
			t.Errorf("%q appears twice, in halves %d and %d", o.Mnemonic, prev, o.Half)
		}
		seen[o.Mnemonic] = o.Half
	}
	fp, in := x86tbl.SSEHalfOps(x86tbl.SSEFloatHalf), x86tbl.SSEHalfOps(x86tbl.SSEIntHalf)
	none := x86tbl.SSEHalfOps(x86tbl.SSENoHalf)
	if got, want := len(fp)+len(in)+len(none), len(x86tbl.SSEOps); got != want {
		t.Errorf("the halves account for %d rows, the table has %d", got, want)
	}
	// The only rows outside both halves are the pair whose direction AT&T
	// decides from the operands; anything else there is a row the self-host
	// silently cannot reach.
	for _, o := range none {
		if o.Mnemonic != "movdqa" && o.Mnemonic != "movdqu" {
			t.Errorf("%q is in neither half — the self-host has no table entry for it, and only movdqa/movdqu are meant to be handled outside the tables", o.Mnemonic)
		}
	}
	if len(none) != 2 {
		t.Errorf("%d rows outside the halves, want exactly movdqa and movdqu", len(none))
	}
}

// TestSSEIntHalfIsAll66Prefixed pins what the integer half's doc comment
// claims. A row with the wrong prefix still encodes something, so nothing
// else would catch it.
func TestSSEIntHalfIsAll66Prefixed(t *testing.T) {
	for _, o := range x86tbl.SSEHalfOps(x86tbl.SSEIntHalf) {
		if o.Prefix != 0x66 {
			t.Errorf("%q is in the packed-integer half with prefix %#02x, but that half is documented as all 66-prefixed", o.Mnemonic, o.Prefix)
		}
	}
}

// TestNamedRowsAreWellFormed guards the by-name vocabulary's own shape: a
// duplicated AT&T spelling would dispatch to one of its two rows, and a probe
// that does not start with its spelling probes some other row. The self-host
// side of the same rows is internal/testing/e2ecompiler's
// TestSelfHostX86TableRowsMatchGas.
func TestNamedRowsAreWellFormed(t *testing.T) {
	seenATT := map[string]bool{}
	for _, fam := range x86tbl.Named {
		if len(fam.Ops) == 0 {
			t.Errorf("family %q has no rows", fam.Name)
		}
		if (fam.FernFn == "") != (fam.Pack == nil) {
			t.Errorf("family %q: FernFn and Pack go together", fam.Name)
		}
		for _, o := range fam.Ops {
			if o.ATT != "" {
				if seenATT[o.ATT] {
					t.Errorf("AT&T spelling %q is listed twice", o.ATT)
				}
				seenATT[o.ATT] = true
			}
			if o.ATTProbe == "" {
				t.Errorf("%s/%s: the AT&T probe is required", fam.Name, o.ATT)
				continue
			}
			if o.ATT != "" && !strings.HasPrefix(o.ATTProbe, o.ATT) {
				t.Errorf("%s: AT&T probe %q does not start with the spelling", o.ATT, o.ATTProbe)
			}
		}
	}
	for _, g := range x86tbl.Groups {
		if g.ATTProbe == "" {
			t.Errorf("group %q needs a probe template", g.Name)
		}
	}
}
