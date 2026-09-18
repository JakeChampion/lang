package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
	"os/exec"
)

// `-backend ssa` refused 58 of the 105 coreutils on x86-64 and none on arm64,
// because x86_64ssa was missing 63 of the runtime-builtin emitters arm64ssa
// had (#9559). Each refusal was a hard one — "call target(s) the module never
// defines" — so nothing was silently wrong, but the backend could not be
// measured, let alone defaulted to, on most of the corpus.
//
// This holds both ISAs to the whole catalogue. It is a BUILD gate, not a
// behaviour one: what it pins is that every builtin these programs reach has
// an emitter, which is the property that regresses when a helper is added to
// one backend and not the other. What each builtin MEANS is pinned by the
// differential tests beside this one, which compare against the flat emitter.
//
// The catalogue is read from disk rather than listed here, so a new utility is
// covered by existing, and a rename cannot quietly drop one.
func TestSSABackendsBuildEveryCoreutil(t *testing.T) {
	root := repoRootForCoreutils(t)
	srcs, err := filepath.Glob(filepath.Join(root, "coreutils", "*.fern"))
	if err != nil {
		t.Fatalf("glob coreutils: %v", err)
	}
	if len(srcs) < 100 {
		t.Fatalf("found %d coreutils sources, want the whole catalogue — the glob is wrong", len(srcs))
	}
	sort.Strings(srcs)
	bin := buildFernCLI(t)
	outDir := t.TempDir()

	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		t.Run(target, func(t *testing.T) {
			var refused []string
			for _, src := range srcs {
				name := strings.TrimSuffix(filepath.Base(src), ".fern")
				out := filepath.Join(outDir, target+"_"+name)
				cmd := exec.Command(bin, "-target", target, "-backend", "ssa", "-o", out, src)
				cmd.Env = e2eharness.ChildEnv()
				if o, err := cmd.CombinedOutput(); err != nil {
					refused = append(refused, name+": "+firstLine(o))
					continue
				}
				if err := os.Remove(out); err != nil {
					t.Fatalf("remove %s: %v", out, err)
				}
			}
			if len(refused) != 0 {
				t.Errorf("-backend ssa refuses %d of %d coreutils on %s:\n  %s\n\n"+
					"A refusal here is a runtime builtin with no emitter on this backend. "+
					"Add it beside its twin on the other ISA — the side tables in gas.go "+
					"(runtimeHelperEmitters, heapUsingHelpers, runtimeHelperDeps) all need "+
					"the name, and a helper reached only by a tail jump needs its target "+
					"named as a dependency (#9559).",
					len(refused), len(srcs), target, strings.Join(refused, "\n  "))
			}
		})
	}
}

// repoRootForCoreutils walks up from the test's working directory to the
// module root, since `go test` runs in the package directory.
func repoRootForCoreutils(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("no go.mod above %s", dir)
	return ""
}
