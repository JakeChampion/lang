// CI-DARK: FERN_HTTP_CORPUS_DUMP — This flag regenerates the reference data.
package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestHTTPCorpus(t *testing.T) {
	cases := e2eharness.HTTPCorpusCases(t, "testdata/http-corpus")
	src := e2eharness.HTTPCorpusSource(cases)
	got := dynInterpStdout(t, src) + "\n"
	if envflag.On("FERN_HTTP_CORPUS_DUMP") {
		fmt.Print(got)
		t.Skip("dumped the corpus verdicts; not comparing")
	}
	if diffs := e2eharness.HTTPCorpusDiff(cases, got); len(diffs) > 0 {
		t.Fatalf("interp disagrees with the pinned verdicts (regenerate with FERN_HTTP_CORPUS_DUMP=1 if the parser changed on purpose):\n%s", strings.Join(diffs, "\n"))
	}
	backendsAgree(t, src, strings.TrimSpace(got))
}
