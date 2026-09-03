package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/native/x86tbl"
)

const x86NativeFern = "../../examples/self_host/x86_native.fern"

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

// TestGoAssemblerAcceptsEverySpelling closes the loop on the other side: the
// table is only a single source of truth if the Go assembler actually reaches
// every spelling in it, in all three families that share it.
func TestGoAssemblerAcceptsEverySpelling(t *testing.T) {
	for _, cond := range x86tbl.CondSpellings() {
		for _, probe := range []string{
			"l0:\nj" + cond + " l0",
			"set" + cond + " cl",
			"cmov" + cond + " rax, rcx",
		} {
			if _, _, err := x86_64.AssembleProgram(probe+"\n", 0x400000); err != nil {
				t.Errorf("%q: the Go assembler rejects a spelling the shared table lists: %v", probe, err)
			}
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

// TestGoAssemblerAcceptsEverySSEOp closes the loop on the Go side for the SSE
// vocabulary: the table is only a single source of truth if the assembler
// generated from it actually reaches every row.
//
// Both operand shapes, because the table's own doc says the rows are the
// `xmm <- xmm/mem` forms and a row that only worked register-to-register
// would be half a form.
func TestGoAssemblerAcceptsEverySSEOp(t *testing.T) {
	for _, o := range x86tbl.SSEOps {
		for _, probe := range []string{
			o.Mnemonic + " xmm1, xmm2",
			o.Mnemonic + " xmm1, [rax]",
		} {
			if _, _, err := x86_64.AssembleProgram(probe+"\n", 0x400000); err != nil {
				t.Errorf("%q: the Go assembler rejects a form the shared table lists: %v", probe, err)
			}
		}
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

// TestGoAssemblerAcceptsEveryGroupSpelling is the group-table twin: every
// spelling in every ModRM.reg-extension family assembles through the Go
// assembler in the form the family takes, so a spelling in the table is a
// spelling both assemblers reach.
func TestGoAssemblerAcceptsEveryGroupSpelling(t *testing.T) {
	forms := map[string]string{
		x86tbl.ALU.Name:     "%s rax, rcx",
		x86tbl.Shift.Name:   "%s rax, 3",
		x86tbl.Unary.Name:   "%s rax",
		x86tbl.IncDec.Name:  "%s rax",
		x86tbl.BitTest.Name: "%s rax, 3",
	}
	for _, g := range x86tbl.Groups {
		form, ok := forms[g.Name]
		if !ok {
			t.Fatalf("group %q has no probe form here", g.Name)
		}
		for _, sp := range g.Spellings() {
			probe := fmt.Sprintf(form, sp)
			if _, _, err := x86_64.AssembleProgram(probe+"\n", 0x400000); err != nil {
				t.Errorf("%q: %v", probe, err)
			}
		}
	}
	// And the lock set reaches the prefix path.
	for _, sp := range x86tbl.LockableSpellings() {
		probe := "lock " + sp + " qword ptr [rbx], rax"
		if sp == "inc" || sp == "dec" || sp == "not" || sp == "neg" {
			probe = "lock " + sp + " qword ptr [rbx]"
		}
		if _, _, err := x86_64.AssembleProgram(probe+"\n", 0x400000); err != nil {
			t.Errorf("%q: %v", probe, err)
		}
	}
}

// TestGoAssemblerAcceptsEveryNamedRow: every row of the by-name vocabulary
// assembles through the Go assembler in its own probe, so a family added
// to the table without a dispatch arm, or a probe naming a shape the
// encoder refuses, fails here. The self-host side of the same rows is
// internal/e2eselfhost's TestSelfHostX86TableRowsMatchNative.
func TestGoAssemblerAcceptsEveryNamedRow(t *testing.T) {
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
			if o.Probe == "" || o.ATTProbe == "" {
				t.Errorf("%s/%s: both probes are required", fam.Name, o.Intel)
				continue
			}
			if !strings.HasPrefix(o.Probe, o.Intel+" ") && o.Probe != o.Intel {
				t.Errorf("%s: probe %q does not start with the Intel mnemonic", o.Intel, o.Probe)
			}
			if o.ATT != "" && !strings.HasPrefix(o.ATTProbe, o.ATT) {
				t.Errorf("%s: AT&T probe %q does not start with the spelling", o.ATT, o.ATTProbe)
			}
			if _, _, err := x86_64.AssembleProgram(o.Probe+"\n", 0x400000); err != nil {
				t.Errorf("%s (%s): %v", o.Probe, fam.Name, err)
			}
		}
	}
	for _, g := range x86tbl.Groups {
		if g.Probe == "" || g.ATTProbe == "" {
			t.Errorf("group %q needs both probe templates", g.Name)
		}
	}
}
