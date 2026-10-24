package literate

import "strings"

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

func reformatFernBlock(blk block, format func(string) (string, bool)) string {
	raw := strings.Split(blk.rawText, "\n")
	if len(raw) < 2 {
		return blk.rawText
	}
	end := len(raw)
	last := raw[len(raw)-1]
	hasTail := isBacktickFence(last) || last == ""
	if hasTail {
		end--
	}
	inner := raw[1:end]

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
	b.WriteString(strings.Join(raw[:1+bodyStart], "\n"))
	b.WriteByte('\n')
	b.WriteString(strings.TrimRight(formatted, "\n"))
	if hasTail {
		b.WriteByte('\n')
		b.WriteString(raw[len(raw)-1])
	}
	return b.String()
}

func isBacktickFence(line string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= 3 && strings.Trim(t, "`") == ""
}
