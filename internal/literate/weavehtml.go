package literate

import (
	"fmt"
	"sort"
	"strings"
)

// WeaveHTML renders the document as a self-contained, styled HTML page —
// a richer sibling of Weave (Markdown). Prose is rendered through a small
// Markdown subset (headings, paragraphs, lists, blockquotes, rules, and
// inline code / emphasis / links); each chunk definition becomes an
// anchored section with a Knuth-style ⟨name⟩≡ label, its body in a
// highlighted code block, and a cross-reference footer — and every
// `<<ref>>` line is a clickable link to the referenced chunk's
// definition. The output embeds its own CSS, so it opens directly in a
// browser with no external assets.
func (doc *Document) WeaveHTML() string {
	usedIn := doc.usedIn()
	seen := map[string]int{}
	var body strings.Builder
	for _, blk := range doc.blocks {
		switch {
		case blk.isFern && blk.chunk != "":
			seen[blk.chunk]++
			doc.weaveChunkHTML(&body, blk, seen[blk.chunk] > 1, usedIn[blk.chunk])
		case blk.isFern && blk.file != "":
			seen[blk.file]++
			doc.weaveFileHTML(&body, blk, seen[blk.file] > 1)
		case blk.isFern:
			// Display-only fern snippet: highlighted, but not labelled
			// or linked (it isn't part of the tangle).
			body.WriteString(`<pre class="code display"><code>`)
			for _, line := range strings.Split(stripFence(blk.rawText), "\n") {
				body.WriteString(highlightFern(line))
				body.WriteByte('\n')
			}
			body.WriteString("</code></pre>\n")
		default:
			body.WriteString(markdownToHTML(blk.rawText))
		}
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", htmlEscape(doc.htmlTitle()))
	b.WriteString("<style>\n" + weaveCSS + "</style>\n")
	b.WriteString("</head>\n<body>\n<main>\n")
	b.WriteString(body.String())
	b.WriteString("</main>\n</body>\n</html>\n")
	return b.String()
}

// htmlTitle derives a page title from the document's first ATX `# `
// heading, falling back to a generic label.
func (doc *Document) htmlTitle() string {
	for _, blk := range doc.blocks {
		if blk.isFern {
			continue
		}
		for _, line := range strings.Split(blk.rawText, "\n") {
			if h := strings.TrimSpace(line); strings.HasPrefix(h, "# ") {
				return strings.TrimSpace(h[2:])
			}
		}
	}
	return "Fern literate document"
}

func (doc *Document) weaveChunkHTML(b *strings.Builder, blk block, continuation bool, users []string) {
	marker := "≡"
	if continuation {
		marker = "+≡"
	}
	// Only the first definition carries the link anchor; references point
	// at it (a continuation reuses the same id-less label).
	idAttr := ""
	if !continuation {
		idAttr = fmt.Sprintf(" id=%q", chunkAnchor(blk.chunk))
	}
	b.WriteString(`<section class="chunk">` + "\n")
	fmt.Fprintf(b, `<div class="chunk-label"%s><span class="chunk-name">⟨%s⟩</span>%s</div>`+"\n",
		idAttr, htmlEscape(blk.chunk), marker)
	b.WriteString(weaveBodyHTML(blk.lines))
	if note := crossRefNoteHTML(blk.chunk, users); note != "" {
		b.WriteString(note)
	}
	b.WriteString("</section>\n")
}

func (doc *Document) weaveFileHTML(b *strings.Builder, blk block, continuation bool) {
	marker := "≡"
	if continuation {
		marker = "+≡"
	}
	b.WriteString(`<section class="chunk file">` + "\n")
	fmt.Fprintf(b, `<div class="chunk-label"><span class="chunk-name">📄 <code>%s</code></span>%s</div>`+"\n",
		htmlEscape(blk.file), marker)
	b.WriteString(weaveBodyHTML(blk.lines))
	b.WriteString("</section>\n")
}

// weaveBodyHTML renders a chunk / file body as a highlighted code block,
// turning each `<<ref>>` line into a link to the referenced chunk's
// definition (the indentation is preserved outside the link).
func weaveBodyHTML(lines []bodyLine) string {
	var b strings.Builder
	b.WriteString(`<pre class="code"><code>`)
	for _, bl := range lines {
		if indent, ref, ok := chunkRef(bl.text); ok {
			fmt.Fprintf(&b, "%s<a class=\"ref\" href=\"#%s\">⟨%s⟩</a>\n",
				htmlEscape(indent), chunkAnchor(ref), htmlEscape(ref))
			continue
		}
		b.WriteString(highlightFern(deEscapeRef(bl.text)))
		b.WriteByte('\n')
	}
	b.WriteString("</code></pre>\n")
	return b.String()
}

func crossRefNoteHTML(chunk string, users []string) string {
	if len(users) == 0 {
		return ""
	}
	sort.Strings(users)
	links := make([]string, len(users))
	for i, u := range users {
		links[i] = fmt.Sprintf(`<a class="ref" href="#%s">⟨%s⟩</a>`, chunkAnchor(u), htmlEscape(u))
	}
	return fmt.Sprintf(`<p class="xref">⟨%s⟩ is used in %s.</p>`+"\n", htmlEscape(chunk), strings.Join(links, ", "))
}

// chunkAnchor turns a chunk name into a stable HTML id. The root chunk
// `*` and any non-alphanumeric character map to `-` so the id is
// URL-safe; a `chunk-` prefix avoids clashing with other ids.
func chunkAnchor(name string) string {
	var b strings.Builder
	b.WriteString("chunk-")
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// stripFence removes the opening and closing ``` fence lines from a
// display-only fern block's raw text, leaving just the code.
func stripFence(raw string) string {
	lines := strings.Split(raw, "\n")
	if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
		if n := len(lines); n > 0 && strings.HasPrefix(strings.TrimSpace(lines[n-1]), "```") {
			lines = lines[:n-1]
		}
	}
	return strings.Join(lines, "\n")
}
