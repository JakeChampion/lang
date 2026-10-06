package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// `std/string`'s single-byte search path routes through `__memchr`
// (docs/ATLAS-PLATFORM-PLAN.md §3.4 step 2) — the adoption the whole six-backend
// totality effort existed to unblock.
//
// This is a SWAP, not a new feature: `__str_find_from`'s `nLen == 1` arm used to
// be a hand-written byte loop and is now one intrinsic call. So the gate that
// matters is not "does memchr work" — memchr_test.go already sweeps 2572 cases
// against a Go reference on all three native backends — but "did anything about
// the SEARCH FAMILY's observable behaviour change". Every forward scan in the
// module routes through that one arm: contains / index_of / split / splitn /
// split_once / partition / find_all / count / count_matches / replace /
// replace_n / replacen. A single-byte needle now takes a different code path in
// all of them.
//
// The corpus is therefore built from the CALLERS rather than from the
// intrinsic, and each case is checked against Go's own equivalent. Go is a
// legitimate oracle here precisely because these are byte operations on byte
// strings — `strings.Index` and friends answer the same question.

type strSearchCase struct {
	name string
	// expr is a Fern expression printing one line; want is what Go says.
	expr string
	want string
}

// strSearchCases exercises every std/string entry point that reaches
// __str_find_from with a one-byte needle, plus the boundary shapes a byte loop
// and a 16-byte vector kernel disagree about if anyone gets the tail wrong.
func strSearchCases() []strSearchCase {
	var out []strSearchCase
	add := func(name, expr, want string) { out = append(out, strSearchCase{name, expr, want}) }

	q := fernQuote
	// Haystacks chosen to straddle the vector width: under one block, exactly
	// one block, one block plus a partial tail, and a needle at the very last
	// byte of each.
	for _, n := range []int{0, 1, 7, 15, 16, 17, 31, 32, 33, 40} {
		base := strings.Repeat("a", n)
		hay := base + "z"
		add(fmt.Sprintf("index_of_last_n%d", n),
			fmt.Sprintf("(%s).index_of(\"z\")", q(hay)),
			fmt.Sprint(strings.Index(hay, "z")))
		add(fmt.Sprintf("index_of_absent_n%d", n),
			fmt.Sprintf("(%s).index_of(\"q\")", q(base)),
			fmt.Sprint(strings.Index(base, "q")))
		add(fmt.Sprintf("contains_n%d", n),
			fmt.Sprintf("if ((%s).contains(\"z\")) { 1 } else { 0 }", q(hay)),
			"1")
	}

	// The split family — the callers §3.3 named as the reason memchr was picked
	// as the first kernel.
	for _, s := range []string{"", "a", ",", "a,b", "a,b,c", ",a,", ",,", strings.Repeat("a,", 20) + "b"} {
		add("split_"+fmt.Sprint(len(s))+"_"+strings.ReplaceAll(s, ",", "C"),
			fmt.Sprintf("(%s).split(\",\").len()", q(s)),
			fmt.Sprint(len(strings.Split(s, ","))))
	}

	// replace: rebuilds the string around every hit, so a wrong index corrupts
	// output rather than merely miscounting.
	for _, tc := range []struct{ s, old, new string }{
		{"aaa", "a", "b"}, {"abc", "b", "XY"}, {"", "a", "b"},
		{"a,b,c", ",", ";"}, {strings.Repeat("x", 40) + "y", "y", "Z"},
		{"aXbXc", "X", ""},
	} {
		add("replace_"+tc.s+"_"+tc.old,
			fmt.Sprintf("(%s).replace(%s, %s)", q(tc.s), q(tc.old), q(tc.new)),
			strings.ReplaceAll(tc.s, tc.old, tc.new))
	}

	// Non-UTF-8 and NUL: __memchr is a BYTE search and the callers must stay
	// byte-exact. A NUL is an ordinary byte, not a terminator.
	add("index_of_nul", fmt.Sprintf("(%s).index_of(%s)", q("a\x00b"), q("\x00")), "1")
	add("index_of_high", fmt.Sprintf("(%s).index_of(%s)", q("a\xffb"), q("\xff")), "1")
	add("split_nul", fmt.Sprintf("(%s).split(%s).len()", q("a\x00b\x00c"), q("\x00")), "3")

	// A two-byte needle must NOT take the memchr arm — it routes to Two-Way.
	// These are built so that widening the fast path past one byte gives a
	// DIFFERENT answer, which is not automatic: the obvious spelling
	// `("abcabc").index_of("bc")` returns 1 either way, because the needle's
	// first byte sits exactly where the whole needle does. A mutation check
	// caught that the first time round. Each case below puts needle[0]
	// strictly before the real match, or has no match at all.
	add("two_byte_first_byte_earlier",
		fmt.Sprintf("(%s).index_of(%s)", q("abxbc"), q("bc")),
		fmt.Sprint(strings.Index("abxbc", "bc"))) // 3, not 1
	add("two_byte_first_byte_only",
		fmt.Sprintf("(%s).index_of(%s)", q("abx"), q("bz")),
		fmt.Sprint(strings.Index("abx", "bz"))) // -1, not 1
	add("two_byte_repeated_lead",
		fmt.Sprintf("(%s).index_of(%s)", q("aaab"), q("ab")),
		fmt.Sprint(strings.Index("aaab", "ab"))) // 2, not 0
	add("empty_needle", fmt.Sprintf("(%s).index_of(\"\")", q("abc")), "0")

	return out
}

// runStrSearchCorpus compiles one program containing every case and compares
// each printed line with Go's answer.
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

// The arm64 leg matters for the same reason it did for the kernel itself: arm64
// passes a `string` in TWO operand slots where x86-64 uses one, and the callers
// here hand `__memchr` a string that arrived as a parameter rather than as a
// literal.
func TestArm64StringSearchMemchr(t *testing.T) {
	runStrSearchCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

// The wasm leg covers the one representation the natives do not have: a string
// under 8 bytes lives in its two words with no address, so most of the short
// haystacks above take the kernel's scalar path rather than its v128 one.
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
