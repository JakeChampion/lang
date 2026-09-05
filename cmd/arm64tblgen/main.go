// Command arm64tblgen writes the self-host arm64 assembler's vocabulary from
// internal/native/arm64tbl, the table the Go assembler reads directly — the
// arm64 twin of cmd/x86tblgen.
//
// Two kinds of block are rewritten in place in
// examples/self_host/arm64_native.fern: one per Advanced SIMD class, and the
// scalar vocabulary — the family lookup and the index constants the
// dispatch compares it against, a predicate per family, the base-word
// lookups the encoders read, and arm64_gas_known, the allow-list the
// program loop consults. The staleness test fails if the committed output
// stops matching the table.
//
//	go run ./cmd/arm64tblgen examples/self_host/arm64_native.fern
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/native/arm64tbl"
)

// markers brackets one generated block: the class name appears in both.
func markers(t arm64tbl.VecTable) (begin, end string) {
	name := strings.ToUpper(strings.TrimPrefix(t.FernFn, "arm64_"))
	return "// BEGIN GENERATED ARM64 " + name + " TABLE (cmd/arm64tblgen) — do not edit by hand.",
		"// END GENERATED ARM64 " + name + " TABLE"
}

// lookup renders a mnemonic-to-value function as a string match: one arm
// per row and the wildcard returning -1, the shape a hand-written table
// reads as. A string match lowers to the same str_eq chain an if-chain
// would, so the form costs nothing at run time.
func lookup(b *strings.Builder, fn, ret string, rows []string) {
	fmt.Fprintf(b, "function %s(mnem: string): %s {\n    match (mnem) {\n", fn, ret)
	for _, r := range rows {
		fmt.Fprintf(b, "        %s,\n", r)
	}
	b.WriteString("        _ => { return 0 - 1; }\n    }\n}\n")
}

// genTable renders one lookup: the packed arm64_vec_entry(u, opcode, aux)
// form every class but the permute one uses, whose entry is a bare opc.
func genTable(t arm64tbl.VecTable) string {
	var b strings.Builder
	b.WriteString(t.Doc)
	var rows []string
	for _, o := range t.Ops {
		if t.FernFn == "arm64_vpermute_opc" {
			rows = append(rows, fmt.Sprintf("%q => { return %d; }", o.Mnemonic, o.Opcode))
			continue
		}
		rows = append(rows, fmt.Sprintf("%q => { return arm64_vec_entry(%v, %d, %d); }", o.Mnemonic, o.U, o.Opcode, o.Aux))
	}
	lookup(&b, t.FernFn, "i32", rows)
	return b.String()
}

// scalarMarkers brackets the scalar vocabulary block.
const (
	scalarBegin = "// BEGIN GENERATED ARM64 SCALAR VOCABULARY (cmd/arm64tblgen) — do not edit by hand."
	scalarEnd   = "// END GENERATED ARM64 SCALAR VOCABULARY"
)

// predicateName is the Fern predicate a family generates.
func predicateName(f arm64tbl.Family) string { return "arm64_gas_is_" + f.Name }

// familyID is the Fern constant naming a family's index in the table.
func familyID(f arm64tbl.Family) string { return "arm64_fam_" + f.Name }

// spelling is one mnemonic with the index of the family it belongs to.
type spelling struct {
	mnem string
	fam  int
}

// genFamily renders arm64_gas_family: a mnemonic's family index, or -1.
// The lookup narrows by length, then by one byte at a time from the front,
// until at most leafSpellings candidates remain and are compared
// outright — the shape ir.fern's kind_id takes — so a lookup costs a few
// byte tests and one or two string compares instead of a compare against
// every spelling in the table. Each narrowing step is its own function, so
// no function's fork count outgrows the lint limit as the table grows.
func genFamily(b *strings.Builder) {
	byLen := map[int][]spelling{}
	for i, f := range arm64tbl.Scalar {
		for _, o := range f.Ops {
			byLen[len(o.Mnemonic)] = append(byLen[len(o.Mnemonic)], spelling{o.Mnemonic, i})
		}
	}
	lens := make([]int, 0, len(byLen))
	for n := range byLen {
		lens = append(lens, n)
	}
	sort.Ints(lens)
	b.WriteString("// arm64_gas_family is the index in the table of the family a mnemonic\n")
	b.WriteString("// belongs to, or -1: length first, then the leading bytes, then the\n")
	b.WriteString("// spellings left. The predicates below and arm64_gas_known read it.\n")
	b.WriteString("function arm64_gas_family(mnem: string): i32 {\n    var n: i32 = mnem.len();\n")
	for _, n := range lens {
		fmt.Fprintf(b, "    if (n == %d) { return arm64_gas_family_len%d(mnem); }\n", n, n)
	}
	b.WriteString("    return 0 - 1;\n}\n")
	for _, n := range lens {
		genFamilyGroup(b, fmt.Sprintf("arm64_gas_family_len%d", n), 0, byLen[n])
	}
}

// leafSpellings is the largest group compared spelling by spelling rather
// than narrowed by another byte: with the length and the leading bytes
// already fixed, a few compares against near-identical spellings are
// cheaper than another call. A narrowing step inlines only a byte that one
// spelling alone starts with; the lint counts each `&&` as a fork, so
// inlining more would put the wider steps over the complexity limit.
const leafSpellings = 4

// genFamilyGroup renders one narrowing step over `group`, which share
// their first `depth` bytes, as function `name` and the steps under it.
func genFamilyGroup(b *strings.Builder, name string, depth int, group []spelling) {
	fmt.Fprintf(b, "function %s(mnem: string): i32 {\n", name)
	if len(group) <= leafSpellings {
		for _, s := range group {
			fmt.Fprintf(b, "    if (mnem == %q) { return %s(); }\n", s.mnem, familyID(arm64tbl.Scalar[s.fam]))
		}
		b.WriteString("    return 0 - 1;\n}\n")
		return
	}
	byByte := map[byte][]spelling{}
	var order []byte
	for _, s := range group {
		c := s.mnem[depth]
		if _, seen := byByte[c]; !seen {
			order = append(order, c)
		}
		byByte[c] = append(byByte[c], s)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	fmt.Fprintf(b, "    var c: u8 = mnem[%d];\n", depth)
	type step struct {
		name  string
		group []spelling
	}
	var subs []step
	for _, c := range order {
		sub := byByte[c]
		if len(sub) == 1 {
			fmt.Fprintf(b, "    if (c == b'%c' && mnem == %q) { return %s(); }\n", c, sub[0].mnem, familyID(arm64tbl.Scalar[sub[0].fam]))
			continue
		}
		subName := fmt.Sprintf("%s_%s", name, sub[0].mnem[:depth+1])
		fmt.Fprintf(b, "    if (c == b'%c') { return %s(mnem); }\n", c, subName)
		subs = append(subs, step{subName, sub})
	}
	b.WriteString("    return 0 - 1;\n}\n")
	for _, s := range subs {
		genFamilyGroup(b, s.name, depth+1, s.group)
	}
}

// genScalar renders the by-name vocabulary: an index constant and a
// predicate per family, the family lookup they read, the base-word lookup
// for the families whose encoders read one, and arm64_gas_known over all
// of them plus the pattern-matched and table-dispatched rest.
func genScalar() string {
	var b strings.Builder
	for i, f := range arm64tbl.Scalar {
		fmt.Fprintf(&b, "function %s(): i32 { return %d; }\n", familyID(f), i)
	}
	genFamily(&b)
	for _, f := range arm64tbl.Scalar {
		fmt.Fprintf(&b, "// %s: %s.\n", predicateName(f), f.Doc)
		fmt.Fprintf(&b, "function %s(mnem: string): boolean { return arm64_gas_family(mnem) == %s(); }\n", predicateName(f), familyID(f))
		if f.Base == "" {
			continue
		}
		// The shll family's word is a packed kind, an i32 like the class
		// selectors; every other base is an encoding word.
		var rows []string
		if f.Name == "shll" {
			fmt.Fprintf(&b, "// %s maps a widening-shift mnemonic to its packed kind; -1 = not one.\n", f.Base)
			for _, o := range f.Ops {
				rows = append(rows, fmt.Sprintf("%q => { return %d; }", o.Mnemonic, o.Word))
			}
			lookup(&b, f.Base, "i32", rows)
			continue
		}
		fmt.Fprintf(&b, "// %s maps a mnemonic to its base word (every register field zero); -1 = not one.\n", f.Base)
		for _, o := range f.Ops {
			rows = append(rows, fmt.Sprintf("%q => { return 0x%08x; }", o.Mnemonic, o.Word))
		}
		lookup(&b, f.Base, "i64", rows)
	}
	b.WriteString(`
// arm64_gas_known reports whether a mnemonic is one the assembler handles,
// so the program loop can record anything else rather than drop it: the
// by-name families above, the across-lanes and general Advanced SIMD
// classes, and the conditional branches in both spellings.
function arm64_gas_known(mnem: string): boolean {
    if (arm64_gas_family(mnem) >= 0) { return true; }
    if (arm64_across_entry(mnem) >= 0) { return true; }
    if (arm64_gas_vecgen_handles(mnem)) { return true; }
    return arm64_gas_is_bcond(mnem);
}
`)
	return b.String()
}

// rewriteBlock replaces the region between begin and end with gen, when
// the file carries the markers.
func rewriteBlock(src, begin, end, gen string) (string, error) {
	i := strings.Index(src, begin)
	if i < 0 {
		return src, nil
	}
	j := strings.Index(src, end)
	if j < i {
		return "", fmt.Errorf("end marker %q not found after its begin marker", end)
	}
	return src[:i] + begin + "\n" + gen + src[j:], nil
}

// Rewrite replaces every marked block present in src. A file carrying none
// is returned unchanged.
func Rewrite(src string) (string, error) {
	var err error
	for _, t := range arm64tbl.VecTables {
		begin, end := markers(t)
		if src, err = rewriteBlock(src, begin, end, genTable(t)); err != nil {
			return "", err
		}
	}
	return rewriteBlock(src, scalarBegin, scalarEnd, genScalar())
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: arm64tblgen <file.fern>...")
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
