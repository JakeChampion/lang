package literate

import "sort"

// UnusedChunks returns the names of defined chunks that are never
// reached from a tangle root — the `<<*>>` root chunk (single-file
// documents) or any `file=PATH` file-root (multi-file documents). Such
// a chunk contributes to no tangled output: it is dead code, usually a
// typo in a reference (`<<helpre>>` vs `<<helper>>`) or a leftover
// definition. The result is sorted.
//
// Reachability, not mere reference: a chunk referenced only from another
// unreachable chunk is itself reported, so an entire dead subtree
// surfaces. The root chunk is never reported (it is the entry, used
// implicitly by Tangle).
func (doc *Document) UnusedChunks() []string {
	// refs[name] = the chunk names referenced by name's body.
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

	// Roots: the `<<*>>` chunk (if defined) and every chunk a file-root
	// references directly.
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
