package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/fmtsource"
	"github.com/jakechampion/lang/internal/tables/arm64tbl"
)

func markers(t arm64tbl.VecTable) (begin, end string) {
	name := strings.ToUpper(strings.TrimPrefix(t.FernFn, "arm64_"))
	return "// BEGIN GENERATED ARM64 " + name + " TABLE (cmd/arm64tblgen) — do not edit by hand.",
		"// END GENERATED ARM64 " + name + " TABLE"
}

const lintLimit = 10

func allowLongTable(b *strings.Builder, rows int) {
	if rows+1 > lintLimit {
		b.WriteString("// fern-lint: allow cyclomatic-complexity\n// Each table row has one match arm.\n")
	}
}

func lookup(b *strings.Builder, fn, ret string, rows []string) {
	allowLongTable(b, len(rows))
	fmt.Fprintf(b, "function %s(mnem: string): %s {\n    match (mnem) {\n", fn, ret)
	for _, r := range rows {
		fmt.Fprintf(b, "        %s,\n", r)
	}
	b.WriteString("        _ => { return 0 - 1; }\n    }\n}\n")
}

func genTable(t arm64tbl.VecTable) string {
	var b strings.Builder

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

const (
	scalarBegin = "// BEGIN GENERATED ARM64 SCALAR VOCABULARY (cmd/arm64tblgen) — do not edit by hand."
	scalarEnd   = "// END GENERATED ARM64 SCALAR VOCABULARY"
)

func familyID(f arm64tbl.Family) string { return "arm64_fam_" + f.Name }

type spelling struct {
	mnem string
	fam  int
}

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
	b.WriteString("// arm64_gas_family returns the mnemonic family index, or -1 for an unknown mnemonic.\n")
	b.WriteString("function arm64_gas_family(mnem: string): i32 {\n    let n: i32 = mnem.len();\n")
	for _, n := range lens {
		fmt.Fprintf(b, "    if (n == %d) { return arm64_gas_family_len%d(mnem); }\n", n, n)
	}
	b.WriteString("    return 0 - 1;\n}\n")
	for _, n := range lens {
		genFamilyGroup(b, fmt.Sprintf("arm64_gas_family_len%d", n), 0, byLen[n])
	}
}

const leafSpellings = 4

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
	fmt.Fprintf(b, "    let c: u8 = mnem[%d];\n", depth)
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

func genScalar() string {
	var b strings.Builder
	for i, f := range arm64tbl.Scalar {
		fmt.Fprintf(&b, "function %s(): i32 { return %d; }\n", familyID(f), i)
	}
	genFamily(&b)
	for _, f := range arm64tbl.Scalar {
		if f.Base == "" {
			continue
		}
		var rows []string
		if f.Name == "shll" {
			fmt.Fprintf(&b, "// %s returns the packed kind for a shift mnemonic, or -1 for an unknown mnemonic.\n", f.Base)
			for _, o := range f.Ops {
				rows = append(rows, fmt.Sprintf("%q => { return %d; }", o.Mnemonic, o.Word))
			}
			lookup(&b, f.Base, "i32", rows)
			continue
		}
		fmt.Fprintf(&b, "// %s returns the base word with zero register fields, or -1 for an unknown mnemonic.\n", f.Base)
		for _, o := range f.Ops {
			rows = append(rows, fmt.Sprintf("%q => { return 0x%08x; }", o.Mnemonic, o.Word))
		}
		lookup(&b, f.Base, "i64", rows)
	}
	b.WriteString(`
function arm64_gas_known(mnem: string): boolean {
    if (arm64_gas_family(mnem) >= 0) { return true; }
    if (arm64_across_entry(mnem) >= 0) { return true; }
    if (arm64_gas_vecgen_handles(mnem)) { return true; }
    return arm64_gas_is_bcond(mnem);
}
`)
	return b.String()
}

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

func Rewrite(src string) (string, error) {
	found := strings.Contains(src, scalarBegin)
	var err error
	for _, t := range arm64tbl.VecTables {
		begin, end := markers(t)
		found = found || strings.Contains(src, begin)
		if src, err = rewriteBlock(src, begin, end, genTable(t)); err != nil {
			return "", err
		}
	}
	if src, err = rewriteBlock(src, scalarBegin, scalarEnd, genScalar()); err != nil {
		return "", err
	}
	if !found {
		return src, nil
	}
	return fmtsource.Format(src)
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
