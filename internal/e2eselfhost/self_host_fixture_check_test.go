package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Fern fixtures this package embeds in Go string literals are invisible to
// `make check-sources`, which reads examples/self_host/fern.fern and the stdlib
// and nothing else. So a field added to a struct the fixtures name -- ssasem.Func,
// ssa.SFunc, ssa.SBlock, ssa.SInst, semrecords.Record -- type-checks clean
// locally and then fails every self-host shard with E005, at a CI round per
// attempt on the longest lane (#9805).
//
// This gate assembles each fixture exactly as its owning test does and runs the
// native `fern -check` over it. No gcc, no x86_64 tooling, no driver build: the
// fixtures compile or they do not, in seconds.
//
// It checks the source, not the behaviour. A fixture that type-checks and
// asserts the wrong thing is still the owning test's job to catch.
func TestSelfHostFixtureSourcesCheck(t *testing.T) {
	bin := buildFernForFixtureCheck(t)
	dir := copySelfHostTree(t)

	for _, fx := range selfHostFixtureSources(t) {
		t.Run(fx.name, func(t *testing.T) {
			path := filepath.Join(dir, "fixture_check_"+strings.ReplaceAll(fx.name, "-", "_")+".fern")
			if err := os.WriteFile(path, []byte(fx.source), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(bin, "-check", path).CombinedOutput()
			if err != nil {
				t.Fatalf("%s does not type-check: %v\n%s", fx.owner, err, out)
			}
		})
	}
}

type fixtureSource struct{ name, owner, source string }

// selfHostFixtureSources assembles every embedded fixture through its owning
// test's own builder, so the gate cannot drift from what the heavy tests
// compile. Each entry names that owner, because the diagnostic reports a line
// in a generated file and the reader's next question is which Go file wrote it.
func selfHostFixtureSources(t *testing.T) []fixtureSource {
	t.Helper()
	out := []fixtureSource{
		{"physical_rc", "physicalRCBundle in self_host_ssa_rc_test.go", physicalRCBundle(physicalRCCases())},
		{"physical_rc_reject", "TestSelfHostSSAPhysicalRCRejects in self_host_ssa_rc_test.go", physicalRCRejectSource()},
	}

	unitIdx := make([]int, len(unitCases()))
	for i := range unitIdx {
		unitIdx[i] = i
	}
	unitSrc, _ := unitSource(unitIdx)
	out = append(out, fixtureSource{"units", "unitSource in self_host_ssa_units_test.go", unitSrc})

	semIdx := make([]int, len(semanticCases()))
	for i := range semIdx {
		semIdx[i] = i
	}
	semSrc, _ := semanticSource(semIdx)
	out = append(out, fixtureSource{"semantic", "semanticSource in self_host_ssa_semantic_test.go", semSrc})

	for name, tc := range selfHostLifetimeFixtures() {
		src, _ := lifetimeFernFixture(t, tc)
		out = append(out, fixtureSource{"lifetime_" + name, "lifetimeFernFixture in self_host_ssa_lifetime_test.go", src})
	}
	for _, tc := range dependencyVerifyCases() {
		out = append(out, fixtureSource{"deps_" + tc.name, "dependencyVerifySource in self_host_ssa_dependency_verify_test.go", dependencyVerifySource(t, tc)})
	}
	return out
}

func buildFernForFixtureCheck(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build cmd/fern: %v\n%s", err, out)
	}
	return bin
}
