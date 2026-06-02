package literate

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// FileResult is one tangled output file: its declared path (as written
// in the `file=PATH` directive), the generated Fern source, a per-line
// provenance map back to the `.fern.md` document, and whether the block
// carried the `entry` marker.
type FileResult struct {
	Path    string
	Code    string
	LineMap []Line
	IsEntry bool
}

// HasFiles reports whether the document defines any `file=PATH` blocks,
// i.e. whether it tangles to multiple modules rather than the single
// `<<*>>` root.
func (doc *Document) HasFiles() bool { return len(doc.fileOrder) > 0 }

// OutputFiles returns the output paths declared by `file=` blocks, in
// first-definition order.
func (doc *Document) OutputFiles() []string {
	out := make([]string, len(doc.fileOrder))
	copy(out, doc.fileOrder)
	return out
}

// TangleFiles expands every `file=PATH` root into its own Fern source,
// returning one FileResult per distinct path in document order. Each
// file's body is expanded independently (resolving `<<ref>>` chunks),
// so chunk definitions are shared across the whole document while the
// file-roots partition the program into modules. Errors (undefined or
// cyclic chunk reference) are reported against the document.
func (doc *Document) TangleFiles() ([]FileResult, error) {
	if !doc.HasFiles() {
		return nil, &Error{
			Pos: ast.Position{Line: 1, Col: 1},
			Msg: "literate: no `file=PATH` blocks defined (a multi-file document needs at least one ```fern file=… root)",
		}
	}
	results := make([]FileResult, 0, len(doc.fileOrder))
	for _, path := range doc.fileOrder {
		fr := doc.fileIndex[path]
		out, lineMap, err := collect(func(emit emitFn) error {
			return doc.expandBody(fr.body, "", map[string]bool{}, emit)
		})
		if err != nil {
			return nil, err
		}
		results = append(results, FileResult{
			Path:    path,
			Code:    strings.Join(out, "\n"),
			LineMap: lineMap,
			IsEntry: fr.isEntry,
		})
	}
	return results, nil
}

// EntryFile picks the compile entry among the output files: the file
// marked `entry`, or — when none is marked — the sole output file. It
// errors when the choice is ambiguous (multiple `entry` markers, or
// several files with none marked), naming the candidates so the author
// can add an `entry` marker. A caller that can inspect the tangled
// sources (e.g. to find the one defining `main`) may resolve the
// ambiguous case itself instead.
func (doc *Document) EntryFile() (string, error) {
	var marked []string
	for _, p := range doc.fileOrder {
		if doc.fileIndex[p].isEntry {
			marked = append(marked, p)
		}
	}
	switch {
	case len(marked) == 1:
		return marked[0], nil
	case len(marked) > 1:
		return "", &Error{
			Pos: ast.Position{Line: doc.fileIndex[marked[1]].firstLine, Col: 1},
			Msg: fmt.Sprintf("literate: multiple files marked `entry` (%s) — exactly one is the compile entry", strings.Join(marked, ", ")),
		}
	case len(doc.fileOrder) == 1:
		return doc.fileOrder[0], nil
	default:
		return "", &Error{
			Pos: ast.Position{Line: 1, Col: 1},
			Msg: fmt.Sprintf("literate: %d output files (%s) but none marked `entry` — add `entry` to the ```fern file=… block that holds main", len(doc.fileOrder), strings.Join(doc.fileOrder, ", ")),
		}
	}
}
