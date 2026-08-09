// Coverage for spec/core.md: is every documented instruction actually
// reached by a conformance case?
//
// This is the check the grammar's own coverage gate exists for, one
// layer down. Derivation could not see four invented productions in the
// first draft of grammar.ebnf, because nothing used them — and in a
// normative document that is the worst failure available, since an
// invented rule reads exactly like a true one. The same is true of an
// opcode row: TestCoreOpEffectsMatchTheModel proves a row agrees with
// the verifier's model, and proves nothing about whether any Fern
// program ever produces the instruction.
//
// So the corpus is lowered and the emitted instructions are tallied. An
// op the corpus never reaches has to be listed, with a reason, in
// core.md's "Instructions the corpus does not reach" table — and an
// entry there that the corpus HAS started reaching is reported too, so
// the list shrinks as cases are added rather than silently going stale.
package e2e

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

const coreSpecPath = "../../spec/core.md"

var (
	coreRowRe     = regexp.MustCompile("^\\| `([a-z0-9_.]+)` \\| `[^`]*→[^`]*` \\|")
	coreUnseenRe  = regexp.MustCompile("^\\| `([a-z0-9_.]+)` \\| ([^|]+) \\|\\s*$")
	coreUnseenHdr = "## Instructions the corpus does not reach"
)

// readCoreOps returns the documented mnemonics and the unreached list.
func readCoreOps(t *testing.T) (all []string, unreached map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(coreSpecPath)
	if err != nil {
		t.Fatalf("read %s: %v", coreSpecPath, err)
	}
	unreached = map[string]string{}
	inUnreached := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			inUnreached = line == coreUnseenHdr
		}
		if m := coreRowRe.FindStringSubmatch(line); m != nil {
			all = append(all, m[1])
			continue
		}
		if inUnreached {
			if m := coreUnseenRe.FindStringSubmatch(line); m != nil && m[1] != "Op" {
				unreached[m[1]] = strings.TrimSpace(m[2])
			}
		}
	}
	if len(all) == 0 {
		t.Fatalf("%s has no instruction rows — the parse is broken, not the document", coreSpecPath)
	}
	if len(unreached) == 0 {
		t.Fatalf("%s has no %q table — the parse is broken, not the document", coreSpecPath, coreUnseenHdr)
	}
	return all, unreached
}

func TestCoreOpsAreReachedByTheCorpus(t *testing.T) {
	all, unreached := readCoreOps(t)

	emitted := map[string]int{}
	corpusPrograms(t, func(_ string, _ int, ip *ir.Program) {
		for _, f := range ip.Funcs {
			for _, op := range f.Ops {
				emitted[op.Kind.String()]++
			}
		}
	})
	if len(emitted) == 0 {
		t.Fatal("nothing lowered — the tally is not measuring anything")
	}

	var missing, stale []string
	for _, op := range all {
		_, listed := unreached[op]
		switch {
		case emitted[op] > 0 && listed:
			stale = append(stale, op)
		case emitted[op] == 0 && !listed:
			missing = append(missing, op)
		}
	}
	for op := range unreached {
		if !slices.Contains(all, op) {
			t.Errorf("%q is listed as unreached but is not a documented instruction", op)
		}
	}

	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("no conformance case reaches these instructions, and %s does not say so: %s\n"+
			"Add a case that emits them, or list each in %q with the reason it is unreachable.",
			coreSpecPath, strings.Join(missing, ", "), coreUnseenHdr)
	}
	if len(stale) > 0 {
		t.Errorf("%s lists these as unreached, but the corpus now emits them: %s\n"+
			"Delete their rows — the gap they document is closed.",
			coreSpecPath, strings.Join(stale, ", "))
	}
	t.Logf("%d of %d instructions reached by the corpus; %d listed as unreachable",
		len(all)-len(unreached), len(all), len(unreached))
}
