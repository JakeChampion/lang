package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// __memchr is the first kernel of docs/ATLAS-PLATFORM-PLAN.md §3 — the
// intrinsic that std/string's single-byte search path is meant to route
// through, and the shape the rest of the SIMD tier is modelled on.
//
// It is differential-tested against a Go reference rather than against
// hand-written expectations, because the failures this class of code actually
// has are at boundaries a human does not think to enumerate: the last byte, a
// match exactly at `from`, a `from` past the end, and — once the body is
// vectorised — every offset where a 16-byte block straddles the end of the
// string. The corpus therefore sweeps LENGTH and POSITION exhaustively over a
// small range rather than sampling interesting-looking strings.
//
// That sweep is the point, and it paid: the corpus was written while both
// bodies were still scalar, so when they were replaced with 16-byte vector
// loops (SSE2 on x86-64, NEON on arm64) it already covered every block
// boundary they have, and it passed unchanged. Written afterwards it would
// have been written to fit whatever the new code did.

// memchrRef is the reference semantics, matching the interpreter builtin: the
// first index of `b` at or after `from`, or -1. `from` clamps at 0; a byte
// outside 0..255 never matches.
func memchrRef(s string, b, from int) int {
	if from < 0 {
		from = 0
	}
	if b < 0 || b > 255 {
		return -1
	}
	for i := from; i < len(s); i++ {
		if int(s[i]) == b {
			return i
		}
	}
	return -1
}

func memchrCases() []struct {
	s    string
	b    int
	from int
} {
	type c = struct {
		s    string
		b    int
		from int
	}
	var out []c

	// Exhaustive over length and match position, straddling the 16-byte
	// block size the vector version will use (0..40 covers two full blocks
	// plus a partial tail on either side).
	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		// No match anywhere.
		out = append(out, c{base, 'z', 0})
		// A match at each position, with the scan starting before, at, and
		// after it — `at` is where the needle goes, and the three starts
		// exercise found / found-exactly-at-from / skipped-past.
		for at := 0; at < n; at++ {
			withHit := base[:at] + "z" + base[at+1:]
			out = append(out, c{withHit, 'z', 0})
			out = append(out, c{withHit, 'z', at})
			out = append(out, c{withHit, 'z', at + 1})
		}
	}

	// Edge cases that the sweep above cannot express.
	out = append(out,
		c{"", 'a', 0},                         // empty haystack
		c{"abc", 'a', 100},                    // from past the end
		c{"abc", 'c', -5},                     // negative from clamps to 0
		c{"abc", 256, 0},                      // byte above the range
		c{"abc", -1, 0},                       // byte below the range
		c{"aaa", 'a', 0},                      // first of several
		c{"\x00b\x00", 0, 0},                  // NUL is an ordinary byte, not a terminator
		c{"\x00b\x00", 0, 1},                  // ... and findable after a skip
		c{"\xff\xfe", 0xff, 0},                // high bytes are unsigned, not sign-extended
		c{"\xff\xfe", 0xfe, 0},                //
		c{strings.Repeat("q", 300), 'q', 299}, // long, match at the very end
	)

	// Deterministic randoms over a small alphabet, so matches are dense and
	// land at irregular offsets rather than the neat ones above.
	rng := rand.New(rand.NewSource(20260806))
	for i := 0; i < 60; i++ {
		n := rng.Intn(70)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte('a' + rng.Intn(3)))
		}
		s := sb.String()
		out = append(out, c{s, 'a' + rng.Intn(4), rng.Intn(n + 1)})
	}
	return out
}

func TestX86_64Memchr(t *testing.T) {
	cases := memchrCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((__memchr(%s, %d, %d)).to_string()); write(\"\\n\");\n",
			fernQuote(c.s), c.b, c.from))
		want = append(want, fmt.Sprint(memchrRef(c.s, c.b, c.from)))
	}

	src := `import "std/i32";

function main(): i32 {
` + body.String() + `    return 0;
}
`
	out, exit := compileAndRunX86_64(t, src)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("printed %d lines, want %d — the program and the expectation "+
			"list are out of step, so no comparison below is trustworthy",
			len(got), len(want))
	}
	bad := 0
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("__memchr(%q, %d, %d) = %s, want %s",
					cases[i].s, cases[i].b, cases[i].from, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

// fernQuote renders a Go string as a Fern string literal, escaping the bytes
// the lexer cannot take raw. Bytes above 0x7f are emitted as \xNN so the
// corpus can carry non-UTF-8 haystacks — __memchr is a BYTE search and must
// not care whether its input is valid text.
func fernQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"' || ch == '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case ch == '\n':
			b.WriteString("\\n")
		case ch < 0x20 || ch >= 0x7f:
			fmt.Fprintf(&b, "\\x%02x", ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// The arm64 leg runs the SAME corpus. It is not redundant with the x86-64 one:
// the two kernels differ in both ABI and algorithm, and each difference has
// already produced a real bug.
//
//   - ABI. arm64 uses the two-word string representation, so a `string`
//     argument occupies TWO operand-stack slots where x86-64 uses one. The
//     first version of this call declared three arguments and no types, so
//     arm64 popped three slots for four values and received the LENGTH as its
//     data pointer — an immediate segfault, while x86-64 stayed green.
//
//   - Mask extraction. x86 has pmovmskb (one bit per byte, straight into
//     bsf); NEON has no equivalent and must narrow with `shrn #4`, giving
//     four mask bits per byte, so the lane index is the lowest set bit
//     divided by four. An off-by-a-factor-of-four there is invisible on
//     x86-64 by construction.
//
// Runs under qemu-aarch64 locally and natively on the arm64 CI lane.
func TestArm64Memchr(t *testing.T) {
	cases := memchrCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((__memchr(%s, %d, %d)).to_string()); write(\"\\n\");\n",
			fernQuote(c.s), c.b, c.from))
		want = append(want, fmt.Sprint(memchrRef(c.s, c.b, c.from)))
	}

	src := `import "std/i32";

function main(): i32 {
` + body.String() + `    return 0;
}
`
	out, exit := compileAndRunArm64(t, src)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("printed %d lines, want %d", len(got), len(want))
	}
	bad := 0
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("__memchr(%q, %d, %d) = %s, want %s",
					cases[i].s, cases[i].b, cases[i].from, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}
