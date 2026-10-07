package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Keep this oracle aligned with cmd/ferndoc's moduleSummary. In particular,
// the reference uppercases one byte, even when that byte starts a UTF-8 rune.
func docSummaryReference(intro string) string {
	para, _, _ := strings.Cut(intro, "\n\n")
	s := strings.Join(strings.Fields(para), " ")
	if _, rest, ok := strings.Cut(s, " \u2014 "); ok {
		s = rest
	}
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	first, _, _ := strings.Cut(s, " ")
	if first == "" || strings.ToLower(first) != first || strings.ContainsAny(first, "0123456789") {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func TestSelfHostFerndocDescription(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []string{
		"", "std/x \u2014 a sentence. More.\n\nLater.", "one\u00a0two.",
		"aÉ description.", "élan text.", "bell\a marker.", "zero\u200bwidth.",
		"line\u2028separator.", "emoji \U0001f600.", "quote \" and \\ slash.",
		"first line\ncontinued. More.\n\n- list", "f32 number.", "aİ dotted.",
		"a\u212a kelvin.", "a\u01c5 titlecase.", "\xffinvalid\xc0\x80bytes.",
		"spaces\u0085\u1680\u2000\u2029\u202f\u205f\u3000end.",
		"private\ue000\U000f0000 unassigned\u0378 variation\ufe0f.",
		"controls\x00\x01\b\t\n\v\f\r\x1f\x7f\u0080\u009f.",
	}
	var allBytes strings.Builder
	for b := 0; b < 256; b++ {
		allBytes.WriteByte(byte(b))
	}
	cases = append(cases, allBytes.String())
	var src strings.Builder
	src.WriteString("import \"./docsummary\";\n")
	for i, input := range cases {
		// Separate functions keep the ARM literal pools bounded without dropping
		// any byte assertions, including invalid UTF-8 produced by capitalization.
		fmt.Fprintf(&src, "@noinline\nfunction case_%d(): i32 {\n", i)
		fmt.Fprintf(&src, "let input: string = string_from_bytes_unchecked(%s);\n", etlByteLiteral([]byte(input)))
		fmt.Fprintf(&src, "let summary: string = string_from_bytes_unchecked(%s);\n", etlByteLiteral([]byte(docSummaryReference(input))))
		fmt.Fprintf(&src, "let quoted: string = string_from_bytes_unchecked(%s);\n", etlByteLiteral([]byte(strconv.Quote(input))))
		src.WriteString("if (docsummary.summary(input) != summary) { return 1; }\nif (docsummary.quote(input) != quoted) { return 2; }\nreturn 0;\n}\n")
	}
	src.WriteString("function main(): i32 {\n")
	for i := range cases {
		fmt.Fprintf(&src, "let result_%d: i32 = case_%d();\nif (result_%d != 0) { return %d + result_%d; }\n", i, i, i, 3*i, i)
	}
	src.WriteString("return 0;\n}\n")
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "docsummary.fern")
	path := filepath.Join(dir, "description.fern")
	if err := os.WriteFile(path, []byte(src.String()), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("case %d check %d: exit %d: %s", (code-1)/3, (code-1)%3+1, code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

// The generated table must track the pinned Go reference across all scalars,
// including unassigned code points; a few documentation fixtures cannot prove it.
func TestFerndocPrintableTableMatchesReference(t *testing.T) {
	data, err := os.ReadFile(e2eharness.RepoPath("compiler", "docprintable.fern"))
	if err != nil {
		t.Fatal(err)
	}
	_, literal, ok := strings.Cut(string(data), "return ")
	if !ok {
		t.Fatal("missing generated ranges")
	}
	literal, _, _ = strings.Cut(literal, ";")
	ranges, err := strconv.Unquote(literal)
	if err != nil || len(ranges)%12 != 0 {
		t.Fatalf("invalid table: %v", err)
	}
	printable := make([]bool, 0x110000)
	previous := uint64(127)
	for i := 0; i < len(ranges); i += 12 {
		lo, e1 := strconv.ParseUint(ranges[i:i+6], 16, 32)
		hi, e2 := strconv.ParseUint(ranges[i+6:i+12], 16, 32)
		if e1 != nil || e2 != nil || lo <= previous || hi < lo || hi >= uint64(len(printable)) {
			t.Fatalf("invalid range at %d", i)
		}
		for r := lo; r <= hi; r++ {
			printable[r] = true
		}
		previous = hi
	}
	for r := rune(128); r < rune(len(printable)); r++ {
		if printable[r] != strconv.IsPrint(r) {
			t.Fatalf("printability differs at U+%04X", r)
		}
	}
}
