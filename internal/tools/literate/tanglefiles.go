package literate

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

type FileResult struct {
	Path    string
	Code    string
	LineMap []Line
	IsEntry bool
}

func (doc *Document) HasFiles() bool { return len(doc.fileOrder) > 0 }

func (doc *Document) OutputFiles() []string {
	out := make([]string, len(doc.fileOrder))
	copy(out, doc.fileOrder)
	return out
}

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
