package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

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
				return
			}
			info, cerr := checker.Check(prog)
			if cerr != nil {
				return
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
