package literate

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Program":       "my-program",
		"The main loop!":   "the-main-loop",
		"  Spaces  here  ": "spaces-here",
		"a/b: c":           "a-b-c",
		"Café déjà":        "caf-d-j",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// A document with two or more headings gets a TOC linking to each
// (skipping headings that appear inside fenced prose code).
func TestWeaveHTMLTableOfContents(t *testing.T) {
	src := strings.Join([]string{
		"# Title",
		"intro",
		"```sh",
		"# not a heading (shell comment in a fenced block)",
		"```",
		"## Section Two",
		"```fern",
		"<<*>>=",
		"fn main() {}",
		"```",
	}, "\n")
	out := Parse(src).WeaveHTML()
	if !strings.Contains(out, `<nav class="toc">`) {
		t.Fatalf("expected a TOC nav:\n%s", out)
	}
	if !strings.Contains(out, `<a href="#title">`) || !strings.Contains(out, `<a href="#section-two">`) {
		t.Errorf("TOC missing heading links:\n%s", out)
	}
	if strings.Contains(out, "not a heading") && strings.Contains(out, `href="#not-a-heading`) {
		t.Errorf("a shell comment inside a fenced block must not become a TOC entry:\n%s", out)
	}
}

// A single-heading document gets no TOC (nothing to navigate).
func TestWeaveHTMLNoTocForSingleHeading(t *testing.T) {
	out := Parse("# Only\n```fern\n<<*>>=\nfn main() {}\n```\n").WeaveHTML()
	if strings.Contains(out, `<nav class="toc">`) {
		t.Errorf("did not expect a TOC for a single heading:\n%s", out)
	}
}

// The chunk index lists every chunk, links a chunk to the chunks that
// reference it, and marks the root and unused chunks.
func TestWeaveHTMLChunkIndex(t *testing.T) {
	src := "```fern\n<<*>>=\n<<used>>\n```\n" +
		"```fern\n<<used>>=\nx\n```\n" +
		"```fern\n<<orphan>>=\ny\n```\n"
	out := Parse(src).WeaveHTML()
	idx := out[strings.Index(out, `<section class="chunk-index">`):]
	if !strings.Contains(idx, `href="#chunk-used"`) {
		t.Errorf("index missing the 'used' chunk link:\n%s", idx)
	}
	// 'used' is referenced by the root → "used in ⟨*⟩".
	if !strings.Contains(idx, "used in") {
		t.Errorf("index should show 'used in' for a referenced chunk:\n%s", idx)
	}
	if !strings.Contains(idx, "(root)") {
		t.Errorf("index should mark the root chunk:\n%s", idx)
	}
	if !strings.Contains(idx, "(unused)") {
		t.Errorf("index should mark the orphan chunk unused:\n%s", idx)
	}
}
