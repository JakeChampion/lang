// Command x86tblgen writes the self-host x86-64 assembler's encoding tables
// from internal/native/x86tbl, the table the Go assembler reads directly.
//
// The two assemblers must agree byte for byte, and every drift found so far
// has been vocabulary rather than encoding logic — one side reaching a
// mnemonic or spelling the other does not. Generating the Fern side from the
// same table removes the class: there is one list, and cmd/x86tblgen's
// staleness test fails if the committed output stops matching it.
//
// Mechanism follows cmd/floattablegen and cmd/unicodegen: rewrite between
// marker comments in place, and keep a test that regenerates and diffs.
//
//	go run ./cmd/x86tblgen examples/self_host/x86_native.fern
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jakechampion/lang/internal/native/x86tbl"
)

// block is one marker-delimited region this command owns.
type block struct {
	begin, end string
	generate   func() string
}

var blocks = []block{
	{
		begin:    "// BEGIN GENERATED CONDITION TABLE (cmd/x86tblgen) — do not edit by hand.",
		end:      "// END GENERATED CONDITION TABLE",
		generate: genCondTable,
	},
}

// genCondTable renders x86_gas_cc_code: the suffix-to-code lookup jCC, setCC
// and cmovCC all dispatch through. Aliases share a line, as they do in the
// table, so the generated source reads the way a hand-written one would.
func genCondTable() string {
	var b strings.Builder
	b.WriteString(`// x86_gas_cc_code maps a condition-suffix spelling to its 4-bit condition
// code, shared by jcc 0F 80+cc, setcc 0F 90+cc and cmovcc 0F 40+cc. Returns
// -1 for a spelling that is not a condition, which is what lets the three
// families dispatch by matching their prefix and looking the rest up here.
function x86_gas_cc_code(cond: string): i32 {
`)
	for _, c := range x86tbl.Conds {
		terms := make([]string, 0, len(c.Spellings))
		for _, s := range c.Spellings {
			terms = append(terms, fmt.Sprintf("cond == %q", s))
		}
		fmt.Fprintf(&b, "    if (%s) { return %d; }\n", strings.Join(terms, " || "), c.Code)
	}
	b.WriteString("    return 0 - 1;\n}\n")
	return b.String()
}

// Rewrite replaces every marked block present in src. A file carrying none is
// returned unchanged.
func Rewrite(src string) (string, error) {
	for _, b := range blocks {
		i := strings.Index(src, b.begin)
		if i < 0 {
			continue
		}
		j := strings.Index(src, b.end)
		if j < i {
			return "", fmt.Errorf("end marker %q not found after its begin marker", b.end)
		}
		src = src[:i] + b.begin + "\n" + b.generate() + src[j:]
	}
	return src, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: x86tblgen <file.fern>...")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		out, err := Rewrite(string(src))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
		if out == string(src) {
			continue
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
