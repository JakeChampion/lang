package arm64_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// A second definition of a named .text label is refused rather than
// silently rebinding every branch that names it — the arm64 half of the
// same backstop the x86-64 assembler carries.
//
// The rebind is what #9290 shipped: read_dir_all's helper body took
// `.Lrda2w`, the prefix remove_dir_all already owned, and a program
// calling both builtins had each body's branches resolving into the other.
// The prefixes are disjoint again; this holds the silence closed, so the
// next helper to pick a taken prefix fails to build.
func TestDuplicateTextLabelIsRefused(t *testing.T) {
	src := ".text\n.global _start\n_start:\n" +
		".Lshared:\n\tmov x0, #0\n\tb .Lshared\n" +
		".Lshared:\n\tmov x8, #93\n\tsvc #0\n"
	a, err := arm64.ParseProgram(src)
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

// One definition of each label assembles: the refusal is about a repeated
// NAME, not about the `.L` spelling.
func TestDistinctTextLabelsAreAccepted(t *testing.T) {
	src := ".text\n.global _start\n_start:\n" +
		".Lone:\n\tmov x0, #0\n\tb .Ltwo\n" +
		".Ltwo:\n\tmov x8, #93\n\tsvc #0\n"
	a, err := arm64.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	if _, err := a.TextLen(); err != nil {
		t.Fatalf("TextLen refused two distinct labels: %v", err)
	}
}

// A NUMERIC local repeats by design — `1:` is scoped to the branches around
// it, which is why defineNumericLabel keeps it out of the name table. The
// refusal must not reach it, or every emitter using the GNU-as local idiom
// stops building.
func TestRepeatedNumericLocalIsAccepted(t *testing.T) {
	src := ".text\n.global _start\n_start:\n" +
		"1:\n\tmov x0, #0\n\tb 1f\n" +
		"1:\n\tmov x8, #93\n\tsvc #0\n"
	a, err := arm64.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	if _, err := a.TextLen(); err != nil {
		t.Fatalf("TextLen refused a repeated numeric local, which is scoped "+
			"rather than named: %v", err)
	}
}
