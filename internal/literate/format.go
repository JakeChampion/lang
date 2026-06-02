package literate

import "strings"

// FormatCode rebuilds the document with each fern chunk / file-root body
// passed through `format`, leaving prose, display-only snippets, fences,
// and chunk headers byte-for-byte unchanged. The reformatted body
// replaces the original only when `format` returns ok; a body that
// `format` declines (a fragment that doesn't parse, or one containing
// `<<ref>>` lines) is preserved verbatim, so the document never gets
// corrupted by partial formatting.
//
// The document is an exact line-partition of its blocks, so rejoining
// them with "\n" reproduces the source — only the touched bodies differ.
func (doc *Document) FormatCode(format func(code string) (string, bool)) string {
	parts := make([]string, len(doc.blocks))
	for i, blk := range doc.blocks {
		if blk.isFern && (blk.chunk != "" || blk.file != "") {
			parts[i] = reformatFernBlock(blk, format)
		} else {
			parts[i] = blk.rawText
		}
	}
	return strings.Join(parts, "\n")
}

// reformatFernBlock reformats the body of one chunk-definition or
// file-root block. It splits the block's raw text into the opening fence
// (+ chunk header, if any), the body, and the closing fence; runs the
// body through format; and reassembles. Anything format declines, or any
// shape it can't confidently split, is returned unchanged.
func reformatFernBlock(blk block, format func(string) (string, bool)) string {
	raw := strings.Split(blk.rawText, "\n")
	if len(raw) < 2 {
		return blk.rawText
	}
	// A trailing all-backticks line is the closing fence; an unclosed
	// block at EOF has none.
	end := len(raw)
	hasClose := isBacktickFence(raw[len(raw)-1])
	if hasClose {
		end--
	}
	inner := raw[1:end] // lines between the fences

	// For a chunk definition, the body starts after the `<<NAME>>=`
	// header (the first non-blank inner line). File roots have no header.
	bodyStart := 0
	if blk.chunk != "" {
		for j, ln := range inner {
			if strings.TrimSpace(ln) != "" {
				bodyStart = j + 1
				break
			}
		}
	}
	if bodyStart > len(inner) {
		return blk.rawText
	}

	body := strings.Join(inner[bodyStart:], "\n")
	formatted, ok := format(body)
	if !ok {
		return blk.rawText
	}

	var b strings.Builder
	b.WriteString(strings.Join(raw[:1+bodyStart], "\n")) // opening fence + header
	b.WriteByte('\n')
	b.WriteString(strings.TrimRight(formatted, "\n"))
	if hasClose {
		b.WriteByte('\n')
		b.WriteString(raw[len(raw)-1])
	}
	return b.String()
}

// isBacktickFence reports whether a line is a code-fence delimiter — a
// run of three or more backticks and nothing else (ignoring surrounding
// whitespace).
func isBacktickFence(line string) bool {
	t := strings.TrimSpace(line)
	return fenceRunLen(t) >= 3 && t == strings.Repeat("`", len(t))
}
