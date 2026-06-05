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
		"<title>My Program</title>",           // title from the first H1
		`<h1 id="my-program">My Program</h1>`, // heading rendered + anchored
		"<strong>bold</strong>",               // inline emphasis
		"<em>italic</em>",                     //
		"<code>code</code>",                   //
		`<a href="https://example.com">link</a>`,
		`id="chunk-`,                            // a chunk definition anchor
		`<a class="ref" href="#chunk-body">`,    // the <<body>> reference links to its def
		`<span class="k">fn</span>`,             // keyword highlight
		`<span class="s">&quot;hi&quot;</span>`, // string highlight + escape
		`<section class="chunk-index">`,         // the chunk index appendix
		"</html>",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("WeaveHTML output missing %q", w)
		}
	}
}

// Woven HTML must not emit dangerous link schemes: a javascript: or
// data: URL in a Markdown link would become a working script-injection
// vector in the self-contained page the CLI advertises as browser-
// openable. Dangerous schemes drop to plain text; http/https/mailto and
// relative links still render as anchors. Regression for L2 in
// docs/ADVERSARIAL-REVIEW-2026-06.md.
func TestRenderEmphasisLinkSchemeAllowlist(t *testing.T) {
	for _, in := range []string{
		`[x](javascript:alert(1))`,
		`[y](JavaScript:alert(1))`,
		`[z](data:text/html,payload)`,
		`[v](vbscript:msgbox)`,
	} {
		if got := renderEmphasis(in); strings.Contains(got, "<a href") {
			t.Errorf("renderEmphasis(%q) = %q, must not emit an anchor for a dangerous scheme", in, got)
		}
	}
	safe := map[string]string{
		`[a](https://example.com)`: `<a href="https://example.com">a</a>`,
		`[b](http://example.com)`:  `<a href="http://example.com">b</a>`,
		`[c](mailto:me@x.com)`:     `<a href="mailto:me@x.com">c</a>`,
		`[d](./rel/path)`:          `<a href="./rel/path">d</a>`,
		`[e](#frag)`:               `<a href="#frag">e</a>`,
	}
	for in, want := range safe {
		if got := renderEmphasis(in); !strings.Contains(got, want) {
			t.Errorf("renderEmphasis(%q) = %q, want it to contain %q", in, got, want)
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
