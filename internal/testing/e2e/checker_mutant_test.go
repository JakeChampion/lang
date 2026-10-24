package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

func TestDifferential_CheckerMutants(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, seed := range diffOracleWindow(t, selfHostDiffSeeds(t)) {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			src := fernsmith.GenMain(seed)
			if out, err := selfHostCheck(t, fernBin, stdlibRoot, src); err != nil {
				t.Fatalf("the self-host checker rejects the unmutated program:\n%s\nsrc:\n%s", out, src)
			}
			want := -1
			for i, m := range fernsmith.TypeMutants(src, seed) {
				what := fmt.Sprintf("mutant %d: %q erased", i, m.Ann)
				if !m.Erased {
					what = fmt.Sprintf("mutant %d: %q replaced by %q", i, m.Ann, m.Repl)
				}
				goErr := goCheck(m.Src)
				shOut, shErr := selfHostCheck(t, fernBin, stdlibRoot, m.Src)
				switch {
				case goErr == nil && shErr != nil:
					t.Errorf("%s: the Go checker accepts it and the self-host checker rejects it:\n%s\nsrc:\n%s",
						what, shOut, m.Src)
				case goErr != nil && shErr == nil:
					t.Errorf("%s: the self-host checker accepts it and the Go checker rejects it:\n%v\nsrc:\n%s",
						what, goErr, m.Src)
				case goErr == nil && m.Erased:
					if want < 0 {
						want = runInterpByteOrSkip(t, src)
					}
					if got := runInterpByteOrSkip(t, m.Src); got != want {
						t.Errorf("%s: it checks, but the interpreter returns %d where the original returns %d\nsrc:\n%s",
							what, got, want, m.Src)
					}
				}
			}
		})
	}
}

func goCheck(src string) error {
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		return err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return err
	}
	_, err = checker.Check(prog)
	return err
}

func selfHostCheck(t *testing.T, fernBin, stdlibRoot, src string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out, err := exec.Command(fernBin, "-check", path, stdlibRoot).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
