package e2ecompiler

import (
	"os"
	"regexp"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// ssa.split_tokens gives each side of a uniqueness test a copy of the same
// ops. The arm64 memcpy and memset loops of a copied op must not reuse the
// labels of the other copy. packed_file_tests in this example has such a
// memcpy, and its labels were defined twice.
func TestSelfHostArm64CopiedOpLabelsAreUnique(t *testing.T) {
	cli := buildSelfHostCLI(t)
	asm, err := os.ReadFile(cli.emit(t, e2eharness.RepoPath("examples/tests/ulc_codec.fern"), "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^(\.L[A-Za-z0-9_.$]+):`).FindAllStringSubmatch(string(asm), -1) {
		if seen[m[1]] {
			t.Fatalf("label %s is defined twice", m[1])
		}
		seen[m[1]] = true
	}
	if len(seen) == 0 {
		t.Fatal("the assembly defines no local label; the pattern is wrong")
	}
}
