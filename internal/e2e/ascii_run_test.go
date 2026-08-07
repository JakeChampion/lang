package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// `__ascii_run(s, from)` — the second kernel of docs/ATLAS-PLATFORM-PLAN.md §3,
// and a cheaper one than `__memchr`: `pmovmskb` (x86) and `i8x16.bitmask`
// (wasm) gather the top bit of each byte, which IS the ASCII test, so the
// vector body needs no splat and no compare.
//
// Differential-tested against a Go reference over an exhaustive length ×
// position sweep, for the same reason memchr_test.go is: the failures this
// class of code has are at boundaries nobody enumerates by hand — the last
// byte, a high byte exactly at `from`, a `from` past the end, and every offset
// where a 16-byte block straddles the end of the string.
//
// Two contract differences from __memchr, both load-bearing and both swept
// below:
//   - it returns len(s), not -1, when the rest is ASCII, so a validator can
//     write `i = __ascii_run(s, i)` as a branch-free skip;
//   - the test is the high bit, so EVERY byte >= 0x80 is a hit — including
//     0x80 and 0xFF themselves, which are not valid UTF-8 lead bytes. This
//     intrinsic finds "not ASCII", not "start of a codepoint".

// asciiRunRef is the reference semantics, matching the interpreter builtin.
func asciiRunRef(s string, from int) int {
	if from < 0 {
		from = 0
	}
	for i := from; i < len(s); i++ {
		if s[i] >= 0x80 {
			return i
		}
	}
	return len(s)
}

func asciiRunCases() []struct {
	s    string
	from int
} {
	type c = struct {
		s    string
		from int
	}
	var out []c

	// Exhaustive over length and high-byte position, straddling the 16-byte
	// block size (0..40 covers two full blocks plus a partial tail).
	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		// All ASCII: the answer is n, the branch-free-skip case.
		out = append(out, c{base, 0})
		for at := 0; at < n; at++ {
			hi := base[:at] + "\xc3" + base[at+1:]
			out = append(out, c{hi, 0})
			out = append(out, c{hi, at})     // high byte exactly at `from`
			out = append(out, c{hi, at + 1}) // skipped past it
		}
	}

	// Every high byte is a hit, not just UTF-8 lead bytes. 0x80 is a bare
	// continuation and 0xFF is not legal UTF-8 at all; both must still be
	// found, because this intrinsic answers "not ASCII".
	for _, b := range []byte{0x80, 0xBF, 0xC0, 0xE0, 0xF0, 0xF5, 0xFE, 0xFF} {
		out = append(out, c{"abc" + string([]byte{b}) + "def", 0})
		out = append(out, c{strings.Repeat("x", 20) + string([]byte{b}), 0})
	}

	// 0x7F is the last ASCII byte and must NOT be a hit — the off-by-one that
	// a `>` / `>=` slip on the sign test would produce.
	out = append(out,
		c{"\x7f\x7f\x7f", 0},
		c{strings.Repeat("\x7f", 20), 0},
		c{"", 0},
		c{"abc", 100},   // from past the end -> len
		c{"abc", -5},    // negative from clamps to 0
		c{"\xffabc", 0}, // hit at index 0
		c{strings.Repeat("a", 300) + "\xc3", 300},
	)

	// Deterministic randoms: high bytes at irregular offsets, dense enough
	// that the vector loop and the tail both see hits.
	rng := rand.New(rand.NewSource(20260807))
	for i := 0; i < 60; i++ {
		n := rng.Intn(70)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			if rng.Intn(6) == 0 {
				sb.WriteByte(byte(0x80 + rng.Intn(0x80)))
			} else {
				sb.WriteByte(byte('a' + rng.Intn(3)))
			}
		}
		s := sb.String()
		out = append(out, c{s, rng.Intn(n + 1)})
	}
	return out
}

// runAsciiRunCorpus drives the corpus through one backend and compares every
// answer against asciiRunRef.
func runAsciiRunCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := asciiRunCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((__ascii_run(%s, %d)).to_string()); write(\"\\n\");\n",
			fernQuote(c.s), c.from))
		want = append(want, fmt.Sprint(asciiRunRef(c.s, c.from)))
	}

	out := run(t, `import "std/i32";

function main(): i32 {
`+body.String()+`    return 0;
}
`)
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
				t.Errorf("__ascii_run(%q, %d) = %s, want %s",
					cases[i].s, cases[i].from, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestX86_64AsciiRun(t *testing.T) {
	runAsciiRunCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}
