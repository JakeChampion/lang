package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

func selfHostFixtureSources(t *testing.T) []fixtureSource {
	t.Helper()
	out := []fixtureSource{
		{"physical_rc", "physicalRCBundle in self_host_ssa_rc_test.go", physicalRCBundle(physicalRCCases())},
		{"physical_rc_reject", "TestSelfHostSSAPhysicalRCRejects in self_host_ssa_rc_test.go", physicalRCRejectSource()},
		{"rccertify_cases", "rccertifyCasesSource in self_host_rccertify_test.go", rccertifyCasesSource()},
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

	for _, name := range lifetimeFixtureNames {
		src, _ := lifetimeFixture(t, name)
		out = append(out, fixtureSource{"lifetime_" + name, "testdata/lifetime/" + name + ".fern", src})
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
