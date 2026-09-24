package e2eselfhost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostSemTryOfAVariantLiteral pins two conformance cases whose `?`
// sources are variant literals to the typed path. Under the default mixed
// lowering a refused function falls back to the AST body and the fixture
// still answers right, so the fixture legs cannot see a regression here; the
// tally can. `Some(3.25)?` has no destination, and its payload settles at the
// f64 default only because semsource's `settled` reaches a union's type
// arguments.
func TestSelfHostSemTryOfAVariantLiteral(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, name := range []string{"f64_tryop_widen", "try_binding_takes_its_success_payload"} {
		t.Run(name, func(t *testing.T) {
			caseDir, err := filepath.Abs(filepath.Join("../../conformance/cases", name))
			if err != nil {
				t.Fatal(err)
			}
			wantExit, err := os.ReadFile(filepath.Join(caseDir, "expected.exit"))
			if err != nil {
				t.Fatal(err)
			}
			want, err := strconv.Atoi(strings.TrimSpace(string(wantExit)))
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "prog")
			report, err := semSelfBuild(fernBin, filepath.Join(caseDir, "main.fern"), stdlibRoot, out)
			if err != nil {
				t.Fatal(err)
			}
			if produced, total := semTally(t, report); produced != total {
				t.Fatalf("produced %d of %d declarations on the typed path:\n%s", produced, total, report)
			}
			got := 0
			if err := exec.Command(out).Run(); err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatal(err)
				}
				got = ee.ExitCode()
			}
			if got != want {
				t.Fatalf("exit %d, want %d", got, want)
			}
		})
	}
}
