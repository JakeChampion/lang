package x86_64_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/native/x86tbl"
)

// The no-operand and string vocabulary (#7903 phase 3).
//
// Both assemblers used to hand-list these, in different dialects, with
// nothing comparing them: `cltq` and `cdqe` are the same instruction, so a
// spelling present on one side and absent on the other is invisible to any
// test that checks bytes. Sixteen spellings had gone missing that way.
//
// x86tbl.FixedOps is now the one list, and these tests hold it to gas rather
// than to either assembler — including which SYNTAX MODE accepts each
// spelling, which is the part no comment could enforce.

// asWithSyntax assembles one line under a chosen syntax mode and returns the
// .text bytes, or ok=false when gas rejects it.
func asWithSyntax(t *testing.T, as, objcopy, prologue, line string) ([]byte, bool) {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "in.s")
	oPath := filepath.Join(dir, "in.o")
	binPath := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(sPath, []byte(prologue+".text\n"+line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(as, sPath, "-o", oPath).Run(); err != nil {
		return nil, false
	}
	if out, err := exec.Command(objcopy, "-O", "binary", "--only-section=.text", oPath, binPath).CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, out)
	}
	b, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	return b, true
}

const (
	intelPrologue = ".intel_syntax noprefix\n"
	attPrologue   = ""
)

// TestFixedOpsMatchGNUAs re-derives every row from gas, in the mode the row
// claims each spelling belongs to. A wrong byte, a spelling gas does not
// accept, or a row invented from a manual all fail here.
func TestFixedOpsMatchGNUAs(t *testing.T) {
	as, objcopy := findX86Binutils(t)
	n := 0
	for _, f := range x86tbl.FixedOps {
		for _, m := range []struct {
			name, prologue string
			spellings      []string
		}{
			{"intel", intelPrologue, f.IntelSpellings()},
			{"att", attPrologue, f.ATTSpellings()},
		} {
			for _, s := range m.spellings {
				n++
				t.Run(fmt.Sprintf("%s/%s", m.name, s), func(t *testing.T) {
					got, ok := asWithSyntax(t, as, objcopy, m.prologue, s)
					if !ok {
						t.Fatalf("gas rejects %q in %s syntax, but the table lists it there", s, m.name)
					}
					if !bytes.Equal(got, f.Bytes) {
						t.Fatalf("%s assembles to % x, table says % x", s, got, f.Bytes)
					}
				})
			}
		}
	}
	// Anti-vacuity: a table that lost its rows would pass an empty loop.
	if n < 90 {
		t.Fatalf("only %d (mode, spelling) pairs checked; the table has shrunk", n)
	}
}

// TestFixedOpModeExclusionsAreReal is the other half, and the one that pins
// the rule the two assemblers drifted over: a spelling the table calls
// Intel-only must actually be REJECTED under AT&T, and vice versa.
//
// Without this, moving `stosl` into Both — or `movsd`, which really is in
// both — would silently pass every other gate here, and the generated
// self-host table would grow a spelling gas cannot assemble.
func TestFixedOpModeExclusionsAreReal(t *testing.T) {
	as, objcopy := findX86Binutils(t)
	checked := 0
	for _, f := range x86tbl.FixedOps {
		for _, s := range f.Intel {
			checked++
			if _, ok := asWithSyntax(t, as, objcopy, attPrologue, s); ok {
				t.Errorf("%q is listed Intel-only, but gas accepts it in AT&T syntax too — it belongs in Both", s)
			}
		}
		for _, s := range f.ATT {
			checked++
			if _, ok := asWithSyntax(t, as, objcopy, intelPrologue, s); ok {
				t.Errorf("%q is listed AT&T-only, but gas accepts it under .intel_syntax too — it belongs in Both", s)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no mode-exclusive spellings in the table; the dword string ops are exactly that, so the table is wrong")
	}
}

// TestGoAssemblerAcceptsEveryIntelSpelling closes the loop on this side: the
// table is only worth having if internal/native/x86_64 actually assembles
// through it.
func TestGoAssemblerAcceptsEveryIntelSpelling(t *testing.T) {
	for _, f := range x86tbl.FixedOps {
		for _, s := range f.IntelSpellings() {
			t.Run(s, func(t *testing.T) {
				got, _, err := x86_64.AssembleProgram(s+"\n", elf.TextVAddr)
				if err != nil {
					t.Fatalf("AssembleProgram(%q): %v", s, err)
				}
				if !bytes.Equal(got, f.Bytes) {
					t.Fatalf("%s assembles to % x, want % x", s, got, f.Bytes)
				}
			})
		}
	}
}

// TestFixedOpSpellingsAreDistinct: one spelling may name only one row, in one
// mode. Two rows claiming `movsd` would make the map's winner depend on
// iteration order.
func TestFixedOpSpellingsAreDistinct(t *testing.T) {
	for _, mode := range []struct {
		name string
		get  func(x86tbl.FixedOp) []string
	}{
		{"intel", x86tbl.FixedOp.IntelSpellings},
		{"att", x86tbl.FixedOp.ATTSpellings},
	} {
		seen := map[string]bool{}
		for _, f := range x86tbl.FixedOps {
			for _, s := range mode.get(f) {
				if seen[s] {
					t.Errorf("%s: %q appears in two rows", mode.name, s)
				}
				seen[s] = true
			}
		}
	}
}
