package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// __rmemchr is the third kernel of docs/ATLAS-PLATFORM-PLAN.md §3, and the
// backward sibling §3.3 nominates: the LAST occurrence of a byte at or before
// `from`, or -1.
//
// It ships SCALAR on all seven backends at once, which is §3.4's step 1. This
// corpus is written now, while every body is still a byte loop, for the reason
// __memchr's paid off: written afterwards it would have been written to fit
// whatever the vector code did, and it already sweeps every 16-byte block
// boundary a vector body will later have.
//
// Shared with the forward corpus in spirit but NOT in code, because the two
// differ in the one place a shared generator would paper over: `from` clamps
// DOWN to len-1 here and UP to 0 there, so a negative `from` finds nothing
// here and searches the whole string there.

// rmemchrRef is the reference semantics, matching the interpreter builtin.
func rmemchrRef(s string, b, from int) int {
	if b < 0 || b > 255 {
		return -1
	}
	if from > len(s)-1 {
		from = len(s) - 1
	}
	for i := from; i >= 0; i-- {
		if int(s[i]) == b {
			return i
		}
	}
	return -1
}

func rmemchrCases() []struct {
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

	// Exhaustive over length and match position, straddling the 16-byte block
	// a vector version will use (0..40 is two full blocks plus a partial tail
	// either side).
	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		// No match anywhere.
		out = append(out, c{base, 'z', n})
		// A match at each position, scanning from after / at / before it.
		for at := 0; at < n; at++ {
			withHit := base[:at] + "z" + base[at+1:]
			out = append(out, c{withHit, 'z', n})
			out = append(out, c{withHit, 'z', at})
			out = append(out, c{withHit, 'z', at - 1})
			out = append(out, c{withHit, 'a', at})
		}
	}

	// Edge cases the sweep cannot express.
	out = append(out,
		c{"", 'a', 0},                       // empty haystack
		c{"abc", 'a', 100},                  // from past the end clamps to the last index
		c{"abc", 'c', -1},                   // ... and a negative one finds NOTHING, unlike
		c{"abc", 'a', -5},                   //     __memchr, where it means "the whole string"
		c{"abc", 256, 2},                    // byte above the range
		c{"abc", -1, 2},                     // byte below the range
		c{"aaa", 'a', 2},                    // LAST of several — the property the op exists for
		c{"aba", 'a', 2},                    // a body copied from __memchr answers 0 here
		c{"aba", 'a', 1},                    // ... and 0 here, which is right, so both are needed
		c{"\x00b\x00", 0, 2},                // NUL is an ordinary byte, not a terminator
		c{"\x00b\x00", 0, 1},                // ... and findable walking back past one
		c{"\xff\xfe", 0xff, 1},              // high bytes are unsigned, not sign-extended
		c{"\xff\xfe", 0xfe, 1},              //
		c{strings.Repeat("q", 300), 'q', 0}, // long, match at the very start
	)

	// Deterministic randoms over a small alphabet, so matches are dense and
	// land at irregular offsets.
	rng := rand.New(rand.NewSource(20260831))
	for i := 0; i < 60; i++ {
		n := rng.Intn(70)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte('a' + rng.Intn(3)))
		}
		s := sb.String()
		out = append(out, c{s, 'a' + rng.Intn(4), rng.Intn(n+1) - 1})
	}
	return out
}

// runRmemchrCorpus drives the corpus through one backend and compares every
// answer against rmemchrRef. Shared by every leg for memchr's reason: what
// differs between backends is the kernel, not the expectation.
func runRmemchrCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := rmemchrCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((__rmemchr(%s, %d, %d)).to_string()); write(\"\\n\");\n",
			fernQuote(c.s), c.b, c.from))
		want = append(want, fmt.Sprint(rmemchrRef(c.s, c.b, c.from)))
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
				t.Errorf("__rmemchr(%q, %d, %d) = %s, want %s",
					cases[i].s, cases[i].b, cases[i].from, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestX86_64Rmemchr(t *testing.T) {
	runRmemchrCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64Rmemchr(t *testing.T) {
	runRmemchrCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

// The wasm leg adds a case class neither native leg has: a SHORT string lives
// in its two words with no address at all, so the kernel reads it through
// __fern_str_byte. Every haystack under 8 bytes here exercises that.
func TestWASMRmemchr(t *testing.T) {
	runRmemchrCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}

// The `-backend ssa` (arm64) leg, on the same corpus. It is the backend §3.4
// miscounted and the one an adoption forgets, so it gets the lowering and the
// coverage at the same time as the other six rather than after.
func TestArm64SSARmemchr(t *testing.T) {
	runRmemchrCorpus(t, arm64SSACorpusRunner(t))
}
