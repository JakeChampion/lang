package ir_test

// Corpus-wide determinism guard for IR lowering.
//
// TestLowerDeterministic (determinism_test.go) covers a hand-curated matrix of
// programs chosen to thread the map-walking paths. That is a good smoke test
// and a bad net: it only catches nondeterminism in shapes someone already
// thought to write down. The map-order bug in computePreciseDrops slipped
// through it for exactly that reason — the matrix had no program with two
// locals whose last use falls on the SAME statement, which is what made the
// drop order a coin flip. Five FIXTURES had that shape and had been compiling
// to two different binaries at random for as long as the bug existed.
//
// So this runs the same comparison over the whole conformance corpus, which is
// the closest thing the project has to "real programs" and grows on its own as
// features land. New nondeterminism gets caught by whoever introduces it
// rather than by whoever next attempts a byte-identity comparison — which is
// the expensive failure mode: a nondeterministic compiler makes every such
// comparison require a same-binary control run to separate real diffs from
// noise.
//
// Fixtures that do not load or check (deliberate error cases) are skipped —
// this guard is about lowering, and a program that never reaches lowering has
// nothing to say about it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// lowerFixture loads, checks and lowers one fixture, returning the rendered
// IR. ok is false when the fixture does not reach lowering at all.
func lowerFixture(t *testing.T, path string, ptrW int) (rendered string, ok bool) {
	t.Helper()
	prog, _, err := modload.Load(path)
	if err != nil {
		return "", false
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return "", false
	}
	info, err := checker.Check(prog)
	if err != nil {
		return "", false
	}
	// Monomorphise before lowering, exactly as cmd/fern does. Skipping it
	// leaves generic call sites unresolved and lowering rejects them
	// ("indirect call from non-identifier expression"), which silently
	// dropped 91 of the corpus's most feature-dense fixtures — the
	// std/array + std/json + audit_* set — out of this guard.
	if err := monomorph.Run(prog, info); err != nil {
		return "", false
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	ip, err := ir.LowerWith(prog, info, ptrW)
	ast.RcFreeEnabled = prev
	if err != nil {
		return "", false
	}
	return ip.String(), true
}

func TestLowerDeterministicOverFixtureCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus determinism sweep is not a -short test")
	}
	// The corpus root, spelled out rather than imported: e2eharness.ConformanceCases
	// is the same literal, but importing the e2e harness into a unit-test package
	// to read one constant drags the whole build-and-run machinery in with it.
	// internal/ir sits one level below internal/, like internal/e2e, so the
	// relative path is identical.
	//
	// The count check below is why the move (#6337) was caught the same day
	// rather than months later: a corpus glob that silently matches nothing is
	// the one failure mode of this test that looks exactly like success.
	cases, err := filepath.Glob(filepath.Join("..", "..", "conformance", "cases", "*", "main.fern"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(cases) < 100 {
		t.Fatalf("found only %d fixtures; the corpus glob is wrong", len(cases))
	}

	// Two lowerings per fixture is enough to catch a map-order flip in
	// practice — Go randomises the seed per range, so a two-element bucket
	// disagrees about half the time and the corpus has many such buckets.
	// Repeating more would trade a lot of wall-clock for very little power.
	const rounds = 2
	lowered, skipped := 0, 0
	for _, path := range cases {
		name := filepath.Base(filepath.Dir(path))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		want, ok := lowerFixture(t, path, 8)
		if !ok {
			skipped++
			continue
		}
		lowered++
		for i := 1; i < rounds; i++ {
			got, ok := lowerFixture(t, path, 8)
			if !ok {
				t.Errorf("%s: lowered on round 1 but not round %d", name, i+1)
				break
			}
			if got != want {
				t.Errorf("%s: IR lowering is not deterministic — the same source produced "+
					"different IR on two runs, so the same source compiles to different "+
					"binaries at random. Look for a Go map ranged in a pass whose output "+
					"order matters (see computePreciseDrops).\n%s", name, firstDiff(want, got))
				break
			}
		}
	}
	t.Logf("lowered %d fixtures x%d, skipped %d (did not reach lowering)", lowered, rounds, skipped)
	if lowered < 100 {
		t.Fatalf("only %d fixtures reached lowering; the guard is not covering the corpus", lowered)
	}
}

// firstDiff renders the first differing line of two IR dumps with a little
// context, so a failure names the op that moved instead of printing two
// multi-megabyte programs.
func firstDiff(a, b string) string {
	al, bl := splitLines(a), splitLines(b)
	for i := 0; i < len(al) && i < len(bl); i++ {
		if al[i] != bl[i] {
			lo := i - 3
			if lo < 0 {
				lo = 0
			}
			var sb []byte
			sb = append(sb, "first difference at line "...)
			sb = append(sb, itoa(i+1)...)
			sb = append(sb, ":\n"...)
			for j := lo; j < i; j++ {
				sb = append(sb, "    "+al[j]+"\n"...)
			}
			sb = append(sb, "  - "+al[i]+"\n"...)
			sb = append(sb, "  + "+bl[i]+"\n"...)
			return string(sb)
		}
	}
	return "dumps differ in length only (" + itoa(len(al)) + " vs " + itoa(len(bl)) + " lines)"
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
