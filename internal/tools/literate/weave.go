package literate

import (
	"fmt"
	"sort"
	"strings"
)

// Weave renders the document as a reading-oriented Markdown file. Prose
// and display-only code blocks pass through verbatim; each chunk
// definition is rendered with a Knuth-style label — ⟨name⟩≡ for a
// chunk's first definition, ⟨name⟩+≡ for a continuation — its body
// (with `<<ref>>` lines shown as ⟨ref⟩), and a cross-reference footer
// noting which chunks use it. Because the carrier format is already
// Markdown, the woven output renders directly on GitHub or in any
// Markdown viewer; the value Weave adds over the raw source is the
// chunk labelling and cross-references.
func (doc *Document) Weave() string {
	usedIn := doc.usedIn()
	seen := map[string]int{} // chunk name → definitions emitted so far
	var b strings.Builder
	for bi, blk := range doc.blocks {
		if bi > 0 {
			b.WriteString("\n")
		}
		switch {
		case blk.isFern && blk.chunk != "":
			seen[blk.chunk]++
			continuation := seen[blk.chunk] > 1
			doc.weaveChunk(&b, blk, continuation, usedIn[blk.chunk])
		case blk.isFern && blk.file != "":
			seen[blk.file]++
			doc.weaveFile(&b, blk, seen[blk.file] > 1)
		default:
			// Prose or display-only fern block: verbatim.
			b.WriteString(blk.rawText)
			b.WriteString("\n")
		}
	}
	doc.weaveChunkIndex(&b, usedIn)
	return b.String()
}

// weaveChunkIndex appends a "Chunk index" appendix listing every chunk
// alphabetically with the chunks that reference it (noweb's identifier
// index), marking the root and any unused chunks — mirroring the HTML
// weave's index. Omitted for a trivial (one-chunk) document.
func (doc *Document) weaveChunkIndex(b *strings.Builder, usedIn map[string][]string) {
	names := doc.DefinedChunks()
	if len(names) < 2 {
		return
	}
	sort.Strings(names)
	unused := map[string]bool{}
	for _, u := range doc.UnusedChunks() {
		unused[u] = true
	}
	b.WriteString("\n## Chunk index\n\n")
	for _, n := range names {
		switch {
		case n == RootChunk:
			fmt.Fprintf(b, "- ⟨%s⟩ — *(root)*\n", n)
		case unused[n]:
			fmt.Fprintf(b, "- ⟨%s⟩ — *(unused)*\n", n)
		case len(usedIn[n]) > 0:
			users := append([]string(nil), usedIn[n]...)
			sort.Strings(users)
			labels := make([]string, len(users))
			for i, u := range users {
				labels[i] = "⟨" + u + "⟩"
			}
			fmt.Fprintf(b, "- ⟨%s⟩ — used in %s\n", n, strings.Join(labels, ", "))
		default:
			fmt.Fprintf(b, "- ⟨%s⟩ — *(used by a file root)*\n", n)
		}
	}
}

// weaveChunk renders one chunk-definition block with its label, body,
// and cross-reference footer.
func (doc *Document) weaveChunk(b *strings.Builder, blk block, continuation bool, users []string) {
	label := "⟨" + blk.chunk + "⟩"
	marker := "≡"
	if continuation {
		marker = "+≡"
	}
	fmt.Fprintf(b, "**%s%s**\n\n", label, marker)
	b.WriteString("```fern\n")
	for _, bl := range blk.lines {
		if indent, ref, ok := chunkRef(bl.text); ok {
			fmt.Fprintf(b, "%s⟨%s⟩\n", indent, ref)
			continue
		}
		b.WriteString(deEscapeRef(bl.text))
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	if note := crossRefNote(blk.chunk, users); note != "" {
		b.WriteString(note)
		b.WriteString("\n")
	}
}

// weaveFile renders one `file=PATH` root block with a file label —
// 📄 PATH ≡ for its first piece, +≡ for a continuation — and its body
// (with `<<ref>>` lines shown as ⟨ref⟩).
func (doc *Document) weaveFile(b *strings.Builder, blk block, continuation bool) {
	marker := "≡"
	if continuation {
		marker = "+≡"
	}
	fmt.Fprintf(b, "**📄 `%s`%s**\n\n", blk.file, marker)
	b.WriteString("```fern\n")
	for _, bl := range blk.lines {
		if indent, ref, ok := chunkRef(bl.text); ok {
			fmt.Fprintf(b, "%s⟨%s⟩\n", indent, ref)
			continue
		}
		b.WriteString(deEscapeRef(bl.text))
		b.WriteString("\n")
	}
	b.WriteString("```\n")
}

// crossRefNote produces the italic "⟨X⟩ is used in …" footer for a
// chunk, or empty when nothing references it (e.g. the root chunk).
func crossRefNote(chunk string, users []string) string {
	if len(users) == 0 {
		return ""
	}
	sort.Strings(users)
	labels := make([]string, len(users))
	for i, u := range users {
		labels[i] = "⟨" + u + "⟩"
	}
	return fmt.Sprintf("*⟨%s⟩ is used in %s.*\n", chunk, strings.Join(labels, ", "))
}
