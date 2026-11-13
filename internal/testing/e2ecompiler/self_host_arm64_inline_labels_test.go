package e2ecompiler

import (
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var definedLabelRe = regexp.MustCompile(`(?m)^(\.L\S+):$`)

// The arm64 emitter names the labels of an inline copy, an inline fill and
// a dynamic dispatch chain at each emission. A uniqueness split copies an
// instruction, and the label names once came from its op, so the copy of
// the explicit __memcpy in packed_file_tests defined them two times.
func TestSelfHostArm64InlineLabelsAreUnique(t *testing.T) {
	cli := buildSelfHostCLI(t)
	asm, err := os.ReadFile(cli.emit(t, e2eharness.RepoPath("examples/tests/ulc_codec.fern"), "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	var dup []string
	for _, m := range definedLabelRe.FindAllSubmatch(asm, -1) {
		name := string(m[1])
		seen[name]++
		if seen[name] == 2 {
			dup = append(dup, name)
		}
	}
	sort.Strings(dup)
	if len(dup) > 0 {
		t.Fatalf("labels defined more than once: %v", dup)
	}
}
