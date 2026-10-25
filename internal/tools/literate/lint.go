package literate

import "sort"

// ChunkLine is the line of chunk name's first `<<name>>=` header, or 0.
func (doc *Document) ChunkLine(name string) int {
	if cd := doc.chunks[name]; cd != nil {
		return cd.firstLine
	}
	return 0
}

func (doc *Document) UnusedChunks() []string {
	refs := map[string][]string{}
	for name, cd := range doc.chunks {
		for _, p := range cd.pieces {
			for _, bl := range p.body {
				if _, r, ok := chunkRef(bl.text); ok {
					refs[name] = append(refs[name], r)
				}
			}
		}
	}

	reached := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if reached[n] {
			return
		}
		reached[n] = true
		for _, r := range refs[n] {
			visit(r)
		}
	}

	if _, ok := doc.chunks[RootChunk]; ok {
		visit(RootChunk)
	}
	for _, fr := range doc.fileIndex {
		for _, bl := range fr.body {
			if _, r, ok := chunkRef(bl.text); ok {
				visit(r)
			}
		}
	}

	var unused []string
	for name := range doc.chunks {
		if !reached[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	return unused
}
