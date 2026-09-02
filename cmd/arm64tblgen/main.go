// Command arm64tblgen writes the self-host arm64 assembler's Advanced SIMD
// lookup tables from internal/native/arm64tbl, the table the Go assembler
// reads directly — the arm64 twin of cmd/x86tblgen.
//
// Each class is one marker-delimited block in
// examples/self_host/arm64_native.fern, rewritten in place; the staleness
// test fails if the committed output stops matching the table.
//
//	go run ./cmd/arm64tblgen examples/self_host/arm64_native.fern
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jakechampion/lang/internal/native/arm64tbl"
)

// markers brackets one generated block: the class name appears in both.
func markers(t arm64tbl.VecTable) (begin, end string) {
	name := strings.ToUpper(strings.TrimPrefix(t.FernFn, "arm64_"))
	return "// BEGIN GENERATED ARM64 " + name + " TABLE (cmd/arm64tblgen) — do not edit by hand.",
		"// END GENERATED ARM64 " + name + " TABLE"
}

// genTable renders one lookup: the packed arm64_vec_entry(u, opcode, aux)
// form every class but the permute one uses, whose entry is a bare opc.
func genTable(t arm64tbl.VecTable) string {
	var b strings.Builder
	b.WriteString(t.Doc)
	fmt.Fprintf(&b, "function %s(mnem: string): i32 {\n", t.FernFn)
	for _, o := range t.Ops {
		if t.FernFn == "arm64_vpermute_opc" {
			fmt.Fprintf(&b, "    if (mnem == %q) { return %d; }\n", o.Mnemonic, o.Opcode)
			continue
		}
		fmt.Fprintf(&b, "    if (mnem == %q) { return arm64_vec_entry(%v, %d, %d); }\n", o.Mnemonic, o.U, o.Opcode, o.Aux)
	}
	b.WriteString("    return 0 - 1;\n}\n")
	return b.String()
}

// Rewrite replaces every marked block present in src. A file carrying none
// is returned unchanged.
func Rewrite(src string) (string, error) {
	for _, t := range arm64tbl.VecTables {
		begin, end := markers(t)
		i := strings.Index(src, begin)
		if i < 0 {
			continue
		}
		j := strings.Index(src, end)
		if j < i {
			return "", fmt.Errorf("end marker %q not found after its begin marker", end)
		}
		src = src[:i] + begin + "\n" + genTable(t) + src[j:]
	}
	return src, nil
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
