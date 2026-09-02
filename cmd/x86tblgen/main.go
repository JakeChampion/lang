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
}

// genGPRTables renders the ModRM.reg-extension families — the base
// mnemonic to /digit lookups the suffixed dispatch consults — and the
// lock-prefix set derived from them.
func genGPRTables() string {
	var b strings.Builder
	group := func(fn, doc string, g x86tbl.Group) {
		b.WriteString(doc)
		fmt.Fprintf(&b, "function %s(mnem: string): i32 {\n", fn)
		for _, op := range g.Ops {
			terms := make([]string, 0, len(op.Spellings))
			for _, sp := range op.Spellings {
				terms = append(terms, fmt.Sprintf("mnem == %q", sp))
			}
			fmt.Fprintf(&b, "    if (%s) { return %d; }\n", strings.Join(terms, " || "), op.Ext)
		}
		b.WriteString("    return 0 - 1;\n}\n\n")
	}
	group("x86_gas_alu_ext", `// x86_gas_alu_ext maps an ALU base mnemonic to its group-1 /digit; the
// family's MR opcode row is ext*8 (add 00, or 08, adc 10, sbb 18, and 20,
// sub 28, xor 30, cmp 38).
`, x86tbl.ALU)
	group("x86_gas_unary_ext", `// x86_gas_unary_ext maps an F6/F7-group base mnemonic to its /digit.
`, x86tbl.Unary)
	group("x86_gas_incdec_ext", `// x86_gas_incdec_ext maps inc/dec to their FE/FF-group /digit.
`, x86tbl.IncDec)
	group("x86_gas_shift_ext", `// x86_gas_shift_ext maps a shift/rotate base mnemonic to its C0..D3-group
// /digit. sal is gas's alias for shl and takes shl's /4.
`, x86tbl.Shift)
	group("x86_gas_bt_idx", `// x86_gas_bt_idx maps the bit-test family to its index: the register-form
// opcode is 0xA3 + idx*8 and the immediate-form /digit is 4 + idx.
`, x86tbl.BitTest)
	b.WriteString(`// x86_gas_lockable: the base mnemonics the F0 lock prefix may precede —
// anything else is #UD at runtime, so it is refused at assembly.
function x86_gas_lockable(mnem: string): boolean {
    var names: string[] = [
`)
	// One spelling per line, so the generated source diffs line by line.
	for _, sp := range x86tbl.LockableSpellings() {
		fmt.Fprintf(&b, "        %q,\n", sp)
	}
	b.WriteString(`    ];
    var i: i32 = 0;
    while (i < names.len()) {
        if (names[i] == mnem) { return true; }
        i = i + 1;
    }
    return false;
}
`)
	return b.String()
}

// genFixedTable renders the no-operand vocabulary: the byte lookup the bare
// path consults, and the rep-eligibility predicate.
//
// Only the AT&T spellings are emitted — the self-host assembler reads AT&T,
// so `stosd` (Intel only) must NOT appear here even though it names the same
// bytes as `stosl`. That distinction is the whole reason the table carries a
// mode per spelling rather than one list.
func genFixedTable() string {
	var b strings.Builder
	b.WriteString(`// x86_gas_fixed_op maps a no-operand mnemonic to its bytes, little-endian
// with the byte count in the top byte; -1 when not fixed. gas accepts both
// dialects' spellings of the sign-extend group (cltq and cdqe are one
// instruction), so both are here.
function x86_gas_fixed_op(mnem: string): i32 {
`)
	for _, f := range x86tbl.FixedOps {
		terms := make([]string, 0, 2)
		for _, s := range f.ATTSpellings() {
			terms = append(terms, fmt.Sprintf("mnem == %q", s))
		}
		args := make([]string, 0, 3)
		for _, by := range f.Bytes {
			args = append(args, fmt.Sprint(by))
		}
		fmt.Fprintf(&b, "    if (%s) { return x86_pack%d(%s); }\n",
			strings.Join(terms, " || "), len(f.Bytes), strings.Join(args, ", "))
	}
	b.WriteString("    return 0 - 1;\n}\n")

	b.WriteString(`
// x86_gas_rep_ok reports whether a rep/repne prefix may precede the mnemonic.
// That is the string ops plus ` + "`rep ret`" + ` and ` + "`rep nop`" + `, the two idioms gas also
// takes; anything else it rejects outright, and the prefix would otherwise be
// emitted in front of an instruction that ignores it.
function x86_gas_rep_ok(mnem: string): boolean {
`)
	for _, f := range x86tbl.FixedOps {
		if !f.Repeatable {
			continue
		}
		terms := make([]string, 0, 2)
		for _, s := range f.ATTSpellings() {
			terms = append(terms, fmt.Sprintf("mnem == %q", s))
		}
		fmt.Fprintf(&b, "    if (%s) { return true; }\n", strings.Join(terms, " || "))
	}
	b.WriteString("    return false;\n}\n")
	return b.String()
}

// genSSETables renders the two halves the self-host dispatch consults in
// order, float before integer. The split comes from the table rather than
// being recomputed: which half a mnemonic sits in decides whether an earlier
// arm can claim it first.
//
// A row with no mandatory prefix is written as a bare opcode, not
// `0 * 256 + op`, which is what the hand-written source did — matching it is
// what let the first generated output be diffed against the file it replaced.
func genSSETables() string {
	var b strings.Builder
	b.WriteString("// x86_gas_sse_fp_op: the scalar/packed FLOAT half of the two-byte-opcode\n" +
		"// SSE table (`[pfx] 0F op /r`, xmm destination), packed as pfx*256+op;\n" +
		"// -1 when absent. Mirrors internal/native/x86_64's sseOps.\n")
	writeSSEHalf(&b, "x86_gas_sse_fp_op", x86tbl.SSEFloatHalf)
	b.WriteString("\n// x86_gas_sse_int_op: the packed-INTEGER half of the SSE table (all\n" +
		"// 66-prefixed). The psll/psrl/psra entries here are the by-%xmm-count\n" +
		"// forms; the by-immediate forms are the 0F 71/72/73 groups\n" +
		"// (x86_gas_vshift_op).\n")
	writeSSEHalf(&b, "x86_gas_sse_int_op", x86tbl.SSEIntHalf)
	return b.String()
}

// writeSSEHalf renders one lookup function.
func writeSSEHalf(b *strings.Builder, fn string, half x86tbl.SSEHalf) {
	fmt.Fprintf(b, "function %s(mnem: string): i32 {\n", fn)
	for _, o := range x86tbl.SSEHalfOps(half) {
		if o.Prefix == 0 {
			fmt.Fprintf(b, "    if (mnem == %q) { return %d; }\n", o.Mnemonic, o.Op)
			continue
		}
		fmt.Fprintf(b, "    if (mnem == %q) { return %d * 256 + %d; }\n", o.Mnemonic, o.Prefix, o.Op)
	}
	b.WriteString("    return 0 - 1;\n}\n")
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
