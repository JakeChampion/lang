package literate

import (
	"fmt"
	"strings"
)

// Doctest is one `test`-directive block tangled to a complete, runnable
// Fern program: its body with `<<refs>>` expanded against the document's
// chunks, plus a provenance map back to the `.fern.md` and the block's
// label (the `name=` directive, or a positional default).
type Doctest struct {
	Name    string
	Code    string
	LineMap []Line
}

// HasDoctests reports whether the document defines any `test` blocks.
func (doc *Document) HasDoctests() bool { return len(doc.tests) > 0 }

// Doctests tangles every `test` block into a standalone program. Each is
// expanded like a root (resolving `<<chunk>>` references against the
// shared chunk table), so an example can pull in the very chunks the
// prose is explaining. Errors (undefined / cyclic chunk) carry the
// document line, like Tangle.
func (doc *Document) Doctests() ([]Doctest, error) {
	out := make([]Doctest, 0, len(doc.tests))
	for i, t := range doc.tests {
		lines, lineMap, err := collect(func(emit emitFn) error {
			return doc.expandBody(t.body, "", map[string]bool{}, emit)
		})
		if err != nil {
			return nil, err
		}
		name := t.name
		if name == "" {
			name = fmt.Sprintf("doctest %d (line %d)", i+1, t.firstLine)
		}
		out = append(out, Doctest{Name: name, Code: strings.Join(lines, "\n"), LineMap: lineMap})
	}
	return out, nil
}
