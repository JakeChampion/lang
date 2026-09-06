package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// monomorph re-checks each instantiated clone and used to report ANY failure
// as "monomorph: re-check failed (compiler bug)". Two ordinary user type
// errors reached that path, so a program with a plain mistake accused the
// compiler and gave its author no code to look up (#8452).
//
// Those two are fixed and pinned in internal/monomorph. This is the gate that
// keeps the CLASS closed rather than the two instances: no program in the
// conformance corpus may produce a message naming an internal pass. The
// corpus is the right corpus for it because a third of it is compile-error
// cases — programs deliberately wrong in every way the language can be — so
// it exercises exactly the failures that would land there.
//
// A pass name in a user-facing message is the bug whether or not it also says
// "compiler bug": the author cannot act on either.
func TestMonomorphNeverBlamesTheCompilerOverTheCorpus(t *testing.T) {
	entries, err := os.ReadDir(conformanceCases)
	if err != nil {
		t.Fatalf("read %s: %v", conformanceCases, err)
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		main := filepath.Join(conformanceCases, name, "main.fern")
		if st, serr := os.Stat(main); serr != nil || st.IsDir() {
			continue
		}
		checked++
		t.Run(name, func(t *testing.T) {
			prog, _, lerr := modload.Load(main)
			if lerr != nil {
				return // a load error is the loader's own diagnostic
			}
			info, cerr := checker.Check(prog)
			if cerr != nil {
				return // a checker diagnostic is what a wrong program should get
			}
			if merr := monomorph.Run(prog, info); merr != nil {
				msg := merr.Error()
				for _, banned := range []string{"compiler bug", "monomorph:"} {
					if strings.Contains(msg, banned) {
						t.Errorf("monomorph reported %q to the user:\n%s\n\n"+
							"A user-facing message must be a coded diagnostic the author can act on. "+
							"If this really is an internal invariant failing, the invariant is the bug (#8452).",
							banned, msg)
					}
				}
			}
		})
	}
	if checked < 100 {
		t.Fatalf("only %d corpus cases were checked; the corpus is meant to have hundreds, so this gate is not running", checked)
	}
}
