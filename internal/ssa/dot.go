package ssa

import (
	"fmt"
	"strings"
)

// ToDot renders `f` in Graphviz DOT format. Each Block is a
// labelled node showing its ID, Op list, and terminator;
// edges are labelled with the branch condition where one
// exists (`true` / `false` for BrIf, blank for Br).
//
// Use:
//
//	dot -Tsvg f.dot -o f.svg
//
// Pairs with the upcoming `lang dump-ssa --dot` flag for
// quick CFG visualisation in code review and bug reports.
// The textual Func.String form remains the primary
// representation; DOT is for the graphical case.
func (f *Func) ToDot() string {
	if f == nil {
		return `digraph nil { "<nil func>"; }`
	}
	var b strings.Builder
	fmt.Fprintf(&b, "digraph %s {\n", dotIdent(f.Name))
	b.WriteString("  rankdir=TB;\n")
	b.WriteString("  node [shape=box, fontname=\"monospace\"];\n")

	if f.Entry != nil {
		// Highlight entry block.
		fmt.Fprintf(&b, "  block_%d [style=filled, fillcolor=lightblue];\n", f.Entry.ID)
	}

	for _, blk := range f.Blocks {
		fmt.Fprintf(&b, "  block_%d [label=%q];\n", blk.ID, dotBlockLabel(blk))
	}
	for _, blk := range f.Blocks {
		switch blk.Term.Kind {
		case TermBr:
			if blk.Term.Target != nil {
				fmt.Fprintf(&b, "  block_%d -> block_%d;\n",
					blk.ID, blk.Term.Target.ID)
			}
		case TermBrIf:
			if blk.Term.True != nil {
				fmt.Fprintf(&b, "  block_%d -> block_%d [label=\"true\"];\n",
					blk.ID, blk.Term.True.ID)
			}
			if blk.Term.False != nil {
				fmt.Fprintf(&b, "  block_%d -> block_%d [label=\"false\"];\n",
					blk.ID, blk.Term.False.ID)
			}
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// dotBlockLabel formats a Block's contents for the DOT label.
// Lines are newline-separated; the printer's existing per-Op
// formatter handles the body so the DOT output mirrors the
// text dump.
func dotBlockLabel(blk *Block) string {
	var b strings.Builder
	fmt.Fprintf(&b, "block %d\\l", blk.ID)
	for _, op := range blk.Ops {
		var line strings.Builder
		writeOp(&line, op, blk)
		// DOT label uses \l for left-justified newline.
		b.WriteString(line.String())
		b.WriteString("\\l")
	}
	{
		var line strings.Builder
		writeTerm(&line, &blk.Term)
		b.WriteString(line.String())
		b.WriteString("\\l")
	}
	return b.String()
}

// dotIdent sanitises a function name for use as a DOT graph
// identifier. DOT identifiers are letters/digits/underscores
// or quoted strings; we replace anything else with underscore.
func dotIdent(s string) string {
	if s == "" {
		return "anon"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
