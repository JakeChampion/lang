package literate

import (
	"strings"
	"testing"
)

func TestWeaveHTMLStructure(t *testing.T) {
	src := strings.Join([]string{
		"# My Program",
		"",
		"Intro with **bold**, *italic*, `code`, and a [link](https://example.com).",
		"",
		"```fern",
		"<<*>>=",
		"fn main() {",
		"    <<body>>",
		"}",
		"```",
		"",
		"```fern",
		"<<body>>=",
		`var s: string = "hi";`,
		"```",
	}, "\n")
	out := Parse(src).WeaveHTML()

	wants := []string{
		"<!DOCTYPE html>",
		"<title>My Program</title>", // title from the first H1
		"<h1>My Program</h1>",       // heading rendered
		"<strong>bold</strong>",     // inline emphasis
		"<em>italic</em>",           //
		"<code>code</code>",         //
		`<a href="https://example.com">link</a>`,
		`id="chunk-`,                            // a chunk definition anchor
		`<a class="ref" href="#chunk-body">`,    // the <<body>> reference links to its def
		`<span class="k">fn</span>`,             // keyword highlight
		`<span class="s">&quot;hi&quot;</span>`, // string highlight + escape
		"</html>",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("WeaveHTML output missing %q", w)
		}
	}
}

// A reference must point at the *first* definition's anchor; a
// continuation (`+≡`) reuses the same id-less label.
func TestWeaveHTMLAnchorOnFirstDefinitionOnly(t *testing.T) {
	src := strings.Join([]string{
		"```fern",
		"<<*>>=",
		"<<body>>",
		"```",
		"```fern",
		"<<body>>=",
		"a",
		"```",
		"```fern",
		"<<body>>=", // continuation
		"b",
		"```",
	}, "\n")
	out := Parse(src).WeaveHTML()
	if n := strings.Count(out, `id="chunk-body"`); n != 1 {
		t.Errorf("chunk-body anchor count = %d, want 1 (first definition only)", n)
	}
}

// Multi-backtick inline code spans (```` ```fern ````) render intact
// rather than being split on single backticks.
func TestInlineMarkdownMultiBacktickSpan(t *testing.T) {
	got := inlineMarkdown("A ```` ```fern file=x ```` block here")
	if !strings.Contains(got, "<code>```fern file=x</code>") {
		t.Errorf("multi-backtick span not preserved: %q", got)
	}
}

// Fenced code blocks carried in prose (`sh`, `markdown`, …) render as a
// <pre> rather than mangling their backticks as inline code.
func TestMarkdownFencedCodeBlock(t *testing.T) {
	got := markdownToHTML("Run:\n\n```sh\nfern -interp x.fern.md\n```\n")
	if !strings.Contains(got, `<pre class="code display"><code>`) {
		t.Errorf("fenced block not rendered as <pre>: %q", got)
	}
	if !strings.Contains(got, "fern -interp x.fern.md") {
		t.Errorf("fenced block content missing: %q", got)
	}
}

func TestHighlightFernEscapesAndSpans(t *testing.T) {
	got := highlightFern(`var x: i32 = a < b; // note`)
	for _, w := range []string{
		`<span class="k">var</span>`,
		`<span class="t">i32</span>`,
		`&lt;`, // the `<` is HTML-escaped, never a raw tag
		`<span class="c">// note</span>`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("highlightFern missing %q in %q", w, got)
		}
	}
}

func TestChunkAnchorSanitizes(t *testing.T) {
	cases := map[string]string{
		"*":             "chunk--",
		"the main loop": "chunk-the-main-loop",
		"square":        "chunk-square",
	}
	for in, want := range cases {
		if got := chunkAnchor(in); got != want {
			t.Errorf("chunkAnchor(%q) = %q, want %q", in, got, want)
		}
	}
}
