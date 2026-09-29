package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The parser corpus (#9854): the request fixtures of llhttp and httparse,
// under testdata/http-corpus, each pinned to what std/http's parser makes of
// it. The interpreter is checked against the pin, and every compiled backend
// against the interpreter, so the parser answers the same on all of them;
// the self-host twin is TestSelfHostHTTPCorpus.
//
// The pin is std/http's own rule, not the upstream parser's: where the two
// disagree the corpus file records both, and a change to a pinned verdict is
// a change to the parser that has to be meant. Regenerate the pins with
// FERN_HTTP_CORPUS_DUMP=1, which prints the measured lines instead of
// comparing (CI-dark, like the other dump switches).
func TestHTTPCorpus(t *testing.T) {
	cases := e2eharness.HTTPCorpusCases(t, "testdata/http-corpus")
	src := e2eharness.HTTPCorpusSource(cases)
	got := dynInterpStdout(t, src) + "\n"
	// CI-DARK: FERN_HTTP_CORPUS_DUMP — a regeneration tool, not coverage:
	// it prints the verdicts to re-record the pins and compares nothing.
	if os.Getenv("FERN_HTTP_CORPUS_DUMP") == "1" {
		fmt.Print(got)
		t.Skip("dumped the corpus verdicts; not comparing")
	}
	if diffs := e2eharness.HTTPCorpusDiff(cases, got); len(diffs) > 0 {
		t.Fatalf("interp disagrees with the pinned verdicts (regenerate with FERN_HTTP_CORPUS_DUMP=1 if the parser changed on purpose):\n%s", strings.Join(diffs, "\n"))
	}
	backendsAgree(t, src, strings.TrimSpace(got))
}
