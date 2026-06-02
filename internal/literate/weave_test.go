package literate

import (
	"strings"
	"testing"
)

func TestWeaveLabelsAndCrossRefs(t *testing.T) {
	src := strings.Join([]string{
		"# A program",
		"",
		"```fern",
		"<<*>>=",
		"<<helper>>",
		"fn main() {}",
		"```",
		"",
		"The helper:",
		"",
		"```fern",
		"<<helper>>=",
		"fn helper() {}",
		"```",
	}, "\n")
	out := Parse(src).Weave()

	// Prose passes through verbatim.
	if !strings.Contains(out, "# A program") {
		t.Errorf("woven output dropped prose:\n%s", out)
	}
	// Chunk definitions get Knuth-style ⟨name⟩≡ labels.
	if !strings.Contains(out, "⟨*⟩≡") {
		t.Errorf("missing root chunk label ⟨*⟩≡:\n%s", out)
	}
	if !strings.Contains(out, "⟨helper⟩≡") {
		t.Errorf("missing ⟨helper⟩≡ label:\n%s", out)
	}
	// References inside a chunk render as ⟨ref⟩, not <<ref>>.
	if strings.Contains(out, "<<helper>>") {
		t.Errorf("woven body still shows raw <<helper>>:\n%s", out)
	}
	// Cross-reference footer: ⟨helper⟩ is used in ⟨*⟩.
	if !strings.Contains(out, "⟨helper⟩ is used in ⟨*⟩") {
		t.Errorf("missing cross-reference footer:\n%s", out)
	}
}

// A chunk defined more than once is labelled ≡ on its first definition
// and +≡ on each continuation.
func TestWeaveContinuationMarker(t *testing.T) {
	src := strings.Join([]string{
		"```fern",
		"<<*>>=",
		"<<body>>",
		"```",
		"```fern",
		"<<body>>=",
		"first",
		"```",
		"```fern",
		"<<body>>=",
		"second",
		"```",
	}, "\n")
	out := Parse(src).Weave()
	if !strings.Contains(out, "⟨body⟩≡") {
		t.Errorf("missing first-definition marker ⟨body⟩≡:\n%s", out)
	}
	if !strings.Contains(out, "⟨body⟩+≡") {
		t.Errorf("missing continuation marker ⟨body⟩+≡:\n%s", out)
	}
}

// Display-only fern blocks are woven verbatim (still useful to the
// reader) but carry no chunk label.
func TestWeaveDisplayOnlyVerbatim(t *testing.T) {
	src := strings.Join([]string{
		"```fern",
		"illustrative, not a chunk",
		"```",
		"```fern",
		"<<*>>=",
		"fn main() {}",
		"```",
	}, "\n")
	out := Parse(src).Weave()
	if !strings.Contains(out, "illustrative, not a chunk") {
		t.Errorf("display-only block missing from weave:\n%s", out)
	}
}
