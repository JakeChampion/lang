package main

import (
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
