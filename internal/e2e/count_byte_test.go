package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// __count_byte is the fourth kernel of docs/ATLAS-PLATFORM-PLAN.md §3, and the
// first with no early exit: how many bytes of a string equal a given byte.
//
// It ships SCALAR on all seven backends at once, which is §3.4's step 1. This
// corpus is written now, while every body is still a byte loop, for the reason
// the three before it paid off: written afterwards it would have been written
// to fit whatever the vector code did, and it already sweeps every 16-byte
// block boundary a vector body will later have.
//
// What separates it from its three siblings is what it does NOT have. There is
// no cursor, so there is no clamp — the one thing each of the others gets
// wrong in a different way is simply absent. What replaces it as the thing a
// port can get wrong is the ACCUMULATOR: a body that returns on first match,
// or that loses the running total across a block boundary, passes every
// single-occurrence case and fails only where matches are dense. The corpus is
// weighted accordingly.

// countByteRef is the reference semantics, matching the interpreter builtin.
// Both degenerate answers are honest counts rather than sentinels: an
// out-of-range byte counts 0 because nothing can equal it, and an empty string
// counts 0 because it has no bytes.
func countByteRef(s string, b int) int {
	if b < 0 || b > 255 {
		return 0
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if int(s[i]) == b {
			n++
		}
	}
	return n
}

func countByteCases() []struct {
	s string
	b int
} {
	type c = struct {
		s string
		b int
	}
	var out []c

	// Exhaustive over length and match position, straddling the 16-byte block
	// a vector version will use (0..40 is two full blocks plus a partial tail
	// either side).
	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		out = append(out, c{base, 'z'}) // nothing matches
		out = append(out, c{base, 'a'}) // EVERYTHING matches — n per block
		for at := 0; at < n; at++ {
			withHit := base[:at] + "z" + base[at+1:]
			out = append(out, c{withHit, 'z'}) // exactly one, at each offset
			out = append(out, c{withHit, 'a'}) // and its complement, n-1
		}
	}

	// Dense alternating patterns: the shape that catches an accumulator lost
	// at a block boundary, since every block contributes a partial total.
	for _, n := range []int{15, 16, 17, 31, 32, 33, 47, 48, 64, 300} {
		var ab, aab strings.Builder
		for i := 0; i < n; i++ {
			ab.WriteByte("ab"[i%2])
			aab.WriteByte("aab"[i%3])
		}
		out = append(out, c{ab.String(), 'a'}, c{ab.String(), 'b'})
		out = append(out, c{aab.String(), 'a'}, c{aab.String(), 'b'})
	}

	// x86-64's __count_byte widens to a 32-byte AVX2 main loop with the
	// 16-byte SSE2 loop kept as its tail (this is the kernel behind `wc
	// -l`'s hot loop; issue: 16 bytes/iteration ran 25% more user time than
	// glibc's AVX2 memchr/rawmemchr at the same task), so every length here
	// also needs a hit at each position straddling the 32-byte block
	// boundary and the boundary between the AVX2 loop and its SSE2 tail —
	// lengths just below/at/above one and two 32-byte blocks.
	for _, n := range []int{31, 32, 33, 47, 48, 49, 63, 64, 65, 79, 80, 81} {
		base := strings.Repeat("a", n)
		out = append(out, c{base, 'z'}) // nothing matches
		out = append(out, c{base, 'a'}) // EVERYTHING matches
		for at := 0; at < n; at++ {
			withHit := base[:at] + "z" + base[at+1:]
			out = append(out, c{withHit, 'z'})
			out = append(out, c{withHit, 'a'})
		}
	}

	// Edge cases the sweep cannot express.
	out = append(out,
		c{"", 'a'},                             // empty haystack
		c{"", 0},                               // ... and with the NUL byte
		c{"abc", 256},                          // byte above the range counts 0
		c{"abc", -1},                           // byte below the range counts 0
		c{"aaa", 256},                          // ... even when every byte would match
		c{"\x00b\x00", 0},                      // NUL is an ordinary byte, not a terminator
		c{"\xff\xfe\xff", 0xff},                // high bytes are unsigned, not sign-extended
		c{"\xff\xfe\xff", 0xfe},                //
		c{"\xff\xfe\xff", 0x7f},                // ... and a low byte does not alias a high one
		c{strings.Repeat("q", 300), 'q'},       // long and entirely matching
		c{strings.Repeat("q", 300), 'z'},       // long and entirely not
		c{strings.Repeat("q", 255) + "z", 'z'}, // one match past several whole blocks
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
		out = append(out, c{sb.String(), 'a' + rng.Intn(4)})
	}
	return out
}

// runCountByteCorpus drives the corpus through one backend and compares every
// answer against countByteRef. Shared by every leg for its siblings' reason:
// what differs between backends is the kernel, not the expectation.
func runCountByteCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := countByteCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((__count_byte(%s, %d)).to_string()); write(\"\\n\");\n",
			fernQuote(c.s), c.b))
		want = append(want, fmt.Sprint(countByteRef(c.s, c.b)))
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
				t.Errorf("__count_byte(%q, %d) = %s, want %s",
					cases[i].s, cases[i].b, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestX86_64CountByte(t *testing.T) {
	runCountByteCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64CountByte(t *testing.T) {
	runCountByteCorpus(t, func(t *testing.T, src string) string {
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
func TestWASMCountByte(t *testing.T) {
	runCountByteCorpus(t, func(t *testing.T, src string) string {
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
func TestArm64SSACountByte(t *testing.T) {
	runCountByteCorpus(t, arm64SSACorpusRunner(t))
}
