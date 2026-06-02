package literate

import (
	"strings"
	"testing"
)

// The Markdown weave appends a chunk index listing each chunk with the
// chunks that reference it, marking the root and unused chunks.
func TestWeaveChunkIndex(t *testing.T) {
	src := "```fern\n<<*>>=\n<<used>>\n```\n" +
		"```fern\n<<used>>=\nx\n```\n" +
		"```fern\n<<orphan>>=\ny\n```\n"
	out := Parse(src).Weave()
	idx := out[strings.Index(out, "## Chunk index"):]
	if !strings.Contains(idx, "## Chunk index") {
		t.Fatalf("expected a Chunk index section:\n%s", out)
	}
	for _, want := range []string{
		"⟨*⟩ — *(root)*",
		"⟨used⟩ — used in ⟨*⟩",
		"⟨orphan⟩ — *(unused)*",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("chunk index missing %q:\n%s", want, idx)
		}
	}
}

// A trivial single-chunk document gets no index (nothing to navigate).
func TestWeaveChunkIndexOmittedWhenTrivial(t *testing.T) {
	out := Parse("```fern\n<<*>>=\nfn main() {}\n```\n").Weave()
	if strings.Contains(out, "## Chunk index") {
		t.Errorf("did not expect a chunk index for a one-chunk doc:\n%s", out)
	}
}
