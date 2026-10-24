package e2e

import (
	"fmt"
	"strings"
	"testing"
)

type strSearchCase struct {
	name string
	expr string
	want string
}

func strSearchCases() []strSearchCase {
	var out []strSearchCase
	add := func(name, expr, want string) { out = append(out, strSearchCase{name, expr, want}) }

	q := fernQuote
	for _, n := range []int{0, 1, 7, 15, 16, 17, 31, 32, 33, 40} {
		base := strings.Repeat("a", n)
		hay := base + "z"
		add(fmt.Sprintf("index_of_last_n%d", n),
			fmt.Sprintf("found_at((%s).index_of(\"z\"))", q(hay)),
			fmt.Sprint(strings.Index(hay, "z")))
		add(fmt.Sprintf("index_of_absent_n%d", n),
			fmt.Sprintf("found_at((%s).index_of(\"q\"))", q(base)),
			fmt.Sprint(strings.Index(base, "q")))
		add(fmt.Sprintf("contains_n%d", n),
			fmt.Sprintf("if ((%s).contains(\"z\")) { 1 } else { 0 }", q(hay)),
			"1")
	}

	for _, s := range []string{"", "a", ",", "a,b", "a,b,c", ",a,", ",,", strings.Repeat("a,", 20) + "b"} {
		add("split_"+fmt.Sprint(len(s))+"_"+strings.ReplaceAll(s, ",", "C"),
			fmt.Sprintf("(%s).split(\",\").len()", q(s)),
			fmt.Sprint(len(strings.Split(s, ","))))
	}

	for _, tc := range []struct{ s, old, new string }{
		{"aaa", "a", "b"}, {"abc", "b", "XY"}, {"", "a", "b"},
		{"a,b,c", ",", ";"}, {strings.Repeat("x", 40) + "y", "y", "Z"},
		{"aXbXc", "X", ""},
	} {
		add("replace_"+tc.s+"_"+tc.old,
			fmt.Sprintf("(%s).replace(%s, %s)", q(tc.s), q(tc.old), q(tc.new)),
			strings.ReplaceAll(tc.s, tc.old, tc.new))
	}

	add("index_of_nul", fmt.Sprintf("found_at((%s).index_of(%s))", q("a\x00b"), q("\x00")), "1")
	add("index_of_high", fmt.Sprintf("found_at((%s).index_of(%s))", q("a\xffb"), q("\xff")), "1")
	add("split_nul", fmt.Sprintf("(%s).split(%s).len()", q("a\x00b\x00c"), q("\x00")), "3")

	add("two_byte_first_byte_earlier",
		fmt.Sprintf("found_at((%s).index_of(%s))", q("abxbc"), q("bc")),
		fmt.Sprint(strings.Index("abxbc", "bc")))
	add("two_byte_first_byte_only",
		fmt.Sprintf("found_at((%s).index_of(%s))", q("abx"), q("bz")),
		fmt.Sprint(strings.Index("abx", "bz")))
	add("two_byte_repeated_lead",
		fmt.Sprintf("found_at((%s).index_of(%s))", q("aaab"), q("ab")),
		fmt.Sprint(strings.Index("aaab", "ab")))
	add("empty_needle", fmt.Sprintf("found_at((%s).index_of(\"\"))", q("abc")), "0")

	return out
}

func runStrSearchCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := strSearchCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((%s).to_string()); write(\"\\n\");\n", c.expr))
		want = append(want, c.want)
	}

	out := run(t, `import "std/string";
import "core/int";

// found_at is a search result as Go's strings.Index reports it, -1 when absent.
function found_at(o: Option[i32]): i32 {
    match (o) {
        Some(i) => { return i; },
        None => { return 0 - 1; }
    }
}

function main(): i32 {
`+body.String()+`    return 0;
}
`)
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("printed %d lines, want %d — the program and the expectation list "+
			"are out of step, so no comparison below is trustworthy", len(got), len(want))
	}
	bad := 0
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("%s: %s = %q, want %q", cases[i].name, cases[i].expr,
					strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d std/string search cases checked against Go", len(want))
}

func TestX86_64StringSearchMemchr(t *testing.T) {
	runStrSearchCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64StringSearchMemchr(t *testing.T) {
	runStrSearchCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestWASMStringSearchMemchr(t *testing.T) {
	runStrSearchCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}
