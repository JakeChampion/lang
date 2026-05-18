package lsp

import (
	"hash/fnv"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// compileCache memoises parse + check results by source content.
// Each Server holds one; updateDoc consults it before re-running the
// pipeline, so an editor that re-opens the same file (or a user
// scrubbing through undo / redo) doesn't pay the parse cost twice.
//
// FIFO eviction with a small fixed cap — LRU would be tidier but
// the working set for an LSP session is small (a handful of open
// docs, plus history). 16 is enough to cover undo / redo trains
// and a couple of recently-edited files without holding multi-MB
// AST graphs hostage.
type compileCache struct {
	entries map[uint64]*compileEntry
	order   []uint64
	cap     int
}

type compileEntry struct {
	src   string // kept for hash-collision verification (fnv collides eventually)
	prog  *ast.Program
	info  *checker.Info
	diags []Diagnostic
}

func newCompileCache(capacity int) *compileCache {
	if capacity <= 0 {
		capacity = 16
	}
	return &compileCache{
		entries: make(map[uint64]*compileEntry, capacity),
		cap:     capacity,
	}
}

// get returns the cached compile result for src, or nil when there's
// no entry (or a stale-collision miss — fnv hashing can theoretically
// collide so we verify the stored source matches before returning).
func (c *compileCache) get(src string) *compileEntry {
	if c == nil {
		return nil
	}
	h := hashSource(src)
	e, ok := c.entries[h]
	if !ok || e.src != src {
		return nil
	}
	return e
}

// put records a compile result. Evicts the oldest entry when at cap.
// Duplicate puts (same source seen twice without an eviction) refresh
// the entry in place without adjusting the FIFO order, so the
// not-yet-evicted version stays in its slot.
func (c *compileCache) put(src string, prog *ast.Program, info *checker.Info, diags []Diagnostic) {
	if c == nil {
		return
	}
	h := hashSource(src)
	if existing, ok := c.entries[h]; ok && existing.src == src {
		existing.prog = prog
		existing.info = info
		existing.diags = diags
		return
	}
	c.entries[h] = &compileEntry{src: src, prog: prog, info: info, diags: diags}
	c.order = append(c.order, h)
	if len(c.order) > c.cap {
		// Drop the oldest entry. The h-keyed map could in theory
		// already point at a different src under the same hash; in
		// that (vanishingly rare) case we'd be evicting whatever's
		// there, which is still bounded and correct.
		evict := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, evict)
	}
}

// hashSource produces a 64-bit fingerprint of the source text. Used
// as the cache key + (with a collision check via the stored src) the
// cheap "did anything change?" test in updateDoc.
func hashSource(src string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(src))
	return h.Sum64()
}
