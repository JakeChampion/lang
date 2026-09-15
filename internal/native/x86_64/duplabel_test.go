package x86_64_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/x86_64"
)

// A second definition of a named .text label is refused rather than
// silently rebinding every branch that names it.
//
// The rebind is what #9290 shipped and what it cost: read_dir_all's helper
// body took the `.L` prefix remove_dir_all already owned, both bodies
// landed in one object, and each one's branches resolved into the other —
// a valid-looking image that segfaulted, with nothing reporting it. The
// prefixes are disjoint again, but the silence is the part worth holding:
// the next helper to pick a taken prefix must fail to build.
//
// The error surfaces at layout rather than at the definition, because
// Label returns nothing; TextLen is the first thing every caller reaches.
func TestDuplicateTextLabelIsRefused(t *testing.T) {
	src := ".text\n.globl _start\n_start:\n" +
		".Lshared:\n\tmov rax, 60\n\tjmp .Lshared\n" +
		".Lshared:\n\tmov rdi, 0\n\tsyscall\n"
	a, err := x86_64.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	_, err = a.TextLen()
	if err == nil {
		t.Fatal("TextLen accepted a duplicate .text label; the later definition " +
			"rebinds every branch that names it, which is the silent mislink " +
			"this refusal exists for")
	}
	for _, want := range []string{"duplicate .text label", ".Lshared"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q — it has to say which label, or it "+
				"cannot be acted on", err, want)
		}
	}
}

// The refusal is about a REPEATED name, not about the `.L` spelling: one
// definition of each label assembles, however many there are.
func TestDistinctTextLabelsAreAccepted(t *testing.T) {
	src := ".text\n.globl _start\n_start:\n" +
		".Lone:\n\tmov rax, 60\n\tjmp .Ltwo\n" +
		".Ltwo:\n\tmov rdi, 0\n\tsyscall\n"
	a, err := x86_64.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	if _, err := a.TextLen(); err != nil {
		t.Fatalf("TextLen refused two distinct labels: %v", err)
	}
}
