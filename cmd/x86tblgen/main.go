package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/fmtsource"
	"github.com/jakechampion/lang/internal/tables/x86tbl"
)

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
	{
		begin:    "// BEGIN GENERATED SSE TABLES (cmd/x86tblgen) — do not edit by hand.",
		end:      "// END GENERATED SSE TABLES",
		generate: genSSETables,
	},
	{
		begin:    "// BEGIN GENERATED FIXED-OP TABLE (cmd/x86tblgen) — do not edit by hand.",
		end:      "// END GENERATED FIXED-OP TABLE",
		generate: genFixedTable,
	},
	{
		begin:    "// BEGIN GENERATED GPR GROUP TABLES (cmd/x86tblgen) — do not edit by hand.",
		end:      "// END GENERATED GPR GROUP TABLES",
		generate: genGPRTables,
	},
	{
		begin:    "// BEGIN GENERATED NAMED VOCABULARY (cmd/x86tblgen) — do not edit by hand.",
		end:      "// END GENERATED NAMED VOCABULARY",
		generate: genNamedTables,
	},
}

const lintLimit = 10

func allowLongTable(b *strings.Builder, rows int) {
	if rows+1 > lintLimit {
		b.WriteString("// fern-lint: allow cyclomatic-complexity\n// Each table row has one match arm.\n")
	}
}

func lookup(b *strings.Builder, fn, param string, rows []string) {
	allowLongTable(b, len(rows))
	fmt.Fprintf(b, "function %s(%s: string): i32 {\n    match (%s) {\n", fn, param, param)
	for _, r := range rows {
		fmt.Fprintf(b, "        %s,\n", r)
	}
	b.WriteString("        _ => { return 0 - 1; }\n    }\n}\n")
}

func genNamedTables() string {
	var b strings.Builder
	for _, f := range x86tbl.Named {
		var spellings []string
		for _, o := range f.Ops {
			if o.ATT != "" {
				spellings = append(spellings, fmt.Sprintf("mnem == %q", o.ATT))
			}
		}
		if len(spellings) == 0 {
			continue
		}
		if f.FernFn != "" {
			var rows []string
			for _, o := range f.Ops {
				if o.ATT == "" {
					continue
				}
				rows = append(rows, fmt.Sprintf("%q => { return %d; }", o.ATT, f.Pack(o)))
			}
			lookup(&b, f.FernFn, "mnem", rows)
			continue
		}
		allowLongTable(&b, len(spellings))
		fmt.Fprintf(&b, "function %s(mnem: string): boolean {\n    return ", f.PredicateName())
		for i, t := range spellings {
			if i > 0 {
				if i%4 == 0 {
					b.WriteString("\n        || ")
				} else {
					b.WriteString(" || ")
				}
			}
			b.WriteString(t)
		}
		b.WriteString(";\n}\n")
	}
	return b.String()
}

func genGPRTables() string {
	var b strings.Builder
	group := func(fn string, g x86tbl.Group) {
		var rows []string
		for _, op := range g.Ops {
			for _, sp := range op.Spellings {
				rows = append(rows, fmt.Sprintf("%q => { return %d; }", sp, op.Ext))
			}
		}
		lookup(&b, fn, "mnem", rows)
		b.WriteString("\n")
	}
	group("x86_gas_alu_ext", x86tbl.ALU)
	group("x86_gas_unary_ext", x86tbl.Unary)
	group("x86_gas_incdec_ext", x86tbl.IncDec)
	group("x86_gas_shift_ext", x86tbl.Shift)
	group("x86_gas_bt_idx", x86tbl.BitTest)
	b.WriteString(`function x86_gas_lockable(mnem: string): boolean {
    let names: string[] = [
`)

	for _, sp := range x86tbl.LockableSpellings() {
		fmt.Fprintf(&b, "        %q,\n", sp)
	}
	b.WriteString(`    ];
    let i: i32 = 0;
    while (i < names.len()) {
        if (names[i] == mnem) { return true; }
        i = i + 1;
    }
    return false;
}
`)
	return b.String()
}

func genFixedTable() string {
	var b strings.Builder
	b.WriteString(`// x86_gas_fixed_op returns instruction bytes in little-endian order.
// The high byte stores the byte count. An unknown mnemonic returns -1.
`)
	var rows []string
	for _, f := range x86tbl.FixedOps {
		args := make([]string, 0, 3)
		for _, by := range f.Bytes {
			args = append(args, fmt.Sprint(by))
		}
		for _, s := range f.Spellings {
			rows = append(rows, fmt.Sprintf("%q => { return x86_pack%d(%s); }", s, len(f.Bytes), strings.Join(args, ", ")))
		}
	}
	lookup(&b, "x86_gas_fixed_op", "mnem", rows)

	b.WriteString(`
function x86_gas_rep_ok(mnem: string): boolean {
`)
	for _, f := range x86tbl.FixedOps {
		if !f.Repeatable {
			continue
		}
		terms := make([]string, 0, 2)
		for _, s := range f.Spellings {
			terms = append(terms, fmt.Sprintf("mnem == %q", s))
		}
		fmt.Fprintf(&b, "    if (%s) { return true; }\n", strings.Join(terms, " || "))
	}
	b.WriteString("    return false;\n}\n")
	return b.String()
}

func genSSETables() string {
	var b strings.Builder
	writeSSEHalf(&b, "x86_gas_sse_fp_op", x86tbl.SSEFloatHalf)
	writeSSEHalf(&b, "x86_gas_sse_int_op", x86tbl.SSEIntHalf)
	var terms []string
	for _, m := range x86tbl.SSERegOnly {
		terms = append(terms, fmt.Sprintf("mnem == %q", m))
	}
	fmt.Fprintf(&b, "function x86_gas_sse_reg_only(mnem: string): boolean {\n    return %s;\n}\n", strings.Join(terms, " || "))
	return b.String()
}

func writeSSEHalf(b *strings.Builder, fn string, half x86tbl.SSEHalf) {
	var rows []string
	for _, o := range x86tbl.SSEHalfOps(half) {
		if o.Prefix == 0 {
			rows = append(rows, fmt.Sprintf("%q => { return %d; }", o.Mnemonic, o.Op))
			continue
		}
		rows = append(rows, fmt.Sprintf("%q => { return %d * 256 + %d; }", o.Mnemonic, o.Prefix, o.Op))
	}
	lookup(b, fn, "mnem", rows)
}

func genCondTable() string {
	var b strings.Builder
	b.WriteString(`// x86_gas_cc_code returns the four-bit condition code, or -1 for an unknown suffix.
`)
	var rows []string
	for _, c := range x86tbl.Conds {
		for _, s := range c.Spellings {
			rows = append(rows, fmt.Sprintf("%q => { return %d; }", s, c.Code))
		}
	}
	lookup(&b, "x86_gas_cc_code", "cond", rows)
	return b.String()
}

func Rewrite(src string) (string, error) {
	found := false
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
		found = true
	}
	if !found {
		return src, nil
	}
	return fmtsource.Format(src)
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
