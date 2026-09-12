package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// __sum_bytes is the sixth kernel of docs/ATLAS-PLATFORM-PLAN.md §3, and the
// family's first true reduction: __count_byte reduces over a predicate, where
// this one carries the bytes themselves into a 32-bit accumulator.
//
// It ships SCALAR on all eight backends at once, which is §3.4's step 1, and
// this corpus is written while every body is still a byte loop so that it is
// not written to fit whatever the vector code later does.
//
// Three things a port of this kernel gets wrong, and none of them is the clamp
// its cursored siblings fail on, because there is no cursor:
//
//   - the ACCUMULATOR lost across a block boundary. Every case here is a sum,
//     so any block whose contribution is dropped changes the answer — unlike a
//     search, where only the block holding the hit matters.
//   - SIGN EXTENSION. A byte is unsigned: 0xff contributes 255, not -1. A body
//     that loads with a sign-extending move passes every case whose bytes are
//     ASCII and fails every case above 0x7f, so the corpus is deliberately
//     half high-byte.
//   - the WRAP. The result is exact modulo 2^32, so a backend accumulating in
//     64 bits and returning without truncating agrees on every short string.
//     TestSumBytesWraps is the case that separates them.
func sumBytesRef(s string) int32 {
	var sum uint32
	for i := 0; i < len(s); i++ {
		sum += uint32(s[i])
	}
	return int32(sum)
}

func sumBytesCases() []string {
	var out []string

	// Exhaustive over length, straddling the 16-byte block a vector version
	// uses (0..40 is two full blocks plus a partial tail either side). Each
	// length appears all-ASCII, all-high-byte, and with one high byte planted
	// at every offset — the last of which is what a lost block contribution
	// and a sign-extending load both show up in.
	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		out = append(out, base, strings.Repeat("\xff", n))
		for at := 0; at < n; at++ {
			out = append(out, base[:at]+"\xff"+base[at+1:])
			out = append(out, base[:at]+"\x00"+base[at+1:])
		}
	}

	// The x86-64 sibling widens to a 32-byte AVX2 main loop with a 16-byte
	// SSE2 tail, so the boundary between the two loops needs the same sweep:
	// lengths just below, at and above one and two 32-byte blocks.
	for _, n := range []int{31, 32, 33, 47, 48, 49, 63, 64, 65, 79, 80, 81} {
		base := strings.Repeat("a", n)
		out = append(out, base, strings.Repeat("\xff", n))
		for at := 0; at < n; at++ {
			out = append(out, base[:at]+"\xff"+base[at+1:])
		}
	}

	// Dense mixed patterns: every block contributes a different partial total,
	// which is the shape an accumulator reset at a block boundary fails.
	for _, n := range []int{15, 16, 17, 31, 32, 33, 47, 48, 64, 300} {
		var asc, hi strings.Builder
		for i := 0; i < n; i++ {
			asc.WriteByte(byte(i % 251))
			hi.WriteByte(byte(255 - i%251))
		}
		out = append(out, asc.String(), hi.String())
	}

	// Edge cases the sweep cannot express.
	out = append(out,
		"",                              // empty sums to 0
		"\x00",                          // NUL is an ordinary byte, not a terminator
		"\x00\x00\x00\x00",              // ... and a whole run of them still sums to 0
		"a\x00b",                        // an interior NUL does not truncate the walk
		"\xff",                          // the maximum single byte
		"\x7f\x80",                      // the sign-bit boundary, either side
		"\x80\x80\x80\x80",              // every byte with the high bit set
		strings.Repeat("\xff", 300),     // long and entirely maximal
		strings.Repeat("\x00", 300),     // long and entirely zero
		strings.Repeat("a", 255)+"\xff", // one high byte past several whole blocks
	)

	// Deterministic randoms over the full byte range, so high bytes are dense
	// and land at irregular offsets.
	rng := rand.New(rand.NewSource(20260911))
	for i := 0; i < 60; i++ {
		n := rng.Intn(70)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte(rng.Intn(256)))
		}
		out = append(out, sb.String())
	}
	return out
}

// runSumBytesCorpus drives the corpus through one backend and compares every
// answer against sumBytesRef. Shared by every leg: what differs between
// backends is the kernel, not the expectation.
func runSumBytesCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := sumBytesCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, s := range cases {
		body.WriteString(fmt.Sprintf("    write((__sum_bytes(%s)).to_string()); write(\"\\n\");\n",
			fernQuote(s)))
		want = append(want, fmt.Sprint(sumBytesRef(s)))
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
				t.Errorf("__sum_bytes(%q) = %s, want %s",
					cases[i], strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestX86_64SumBytes(t *testing.T) {
	runSumBytesCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64SumBytes(t *testing.T) {
	runSumBytesCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

// The wasm leg adds a case class neither native leg has: a SHORT string lives
// in its two words with no address at all, so the kernel reads it through
// __fern_str_byte. Every input under 8 bytes here exercises that.
func TestWASMSumBytes(t *testing.T) {
	runSumBytesCorpus(t, func(t *testing.T, src string) string {
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
// coverage at the same time as the other seven rather than after.
func TestArm64SSASumBytes(t *testing.T) {
	runSumBytesCorpus(t, arm64SSACorpusRunner(t))
}

// The wrap, which no case in the corpus above can reach: 2^32 needs more than
// 16 MiB of 0xff, so the input is built at run time rather than written as a
// literal. 255 * 16843010 is 2^32 + 254, so the kernel owes 254 and a backend
// that accumulates in 64 bits without truncating owes 4294967550.
//
// It allocates the string, so it is not folded into the corpus runner: the
// heap assertion below is about the KERNEL allocating nothing, and a 16 MiB
// haystack in the same program would drown it.
const sumBytesWrapSrc = `import "std/i32";

function main(): i32 {
    var big: string = "\xff".repeat(16843010);
    write((__sum_bytes(big)).to_string());
    write("\n");
    return 0;
}
`

func TestX86_64SumBytesWraps(t *testing.T) {
	out, exit := compileAndRunX86_64(t, sumBytesWrapSrc)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	if got := strings.TrimSpace(out); got != "254" {
		t.Errorf("__sum_bytes over 16843010 0xff bytes = %s, want 254 "+
			"(the sum is 2^32 + 254, so anything else means the result "+
			"was not truncated to 32 bits)", got)
	}
}

func TestArm64SumBytesWraps(t *testing.T) {
	out, exit := compileAndRunArm64(t, sumBytesWrapSrc)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	if got := strings.TrimSpace(out); got != "254" {
		t.Errorf("__sum_bytes over 16843010 0xff bytes = %s, want 254 "+
			"(the sum is 2^32 + 254, so anything else means the result "+
			"was not truncated to 32 bits)", got)
	}
}

// Rule 5 of §3.1 in its allocation form: the kernel reads the string it is
// given and builds nothing. A body that reached for a slice or a copy would
// move the bump pointer, and nothing else in this program does.
const sumBytesNoAllocSrc = `import "std/i32";

function main(): i32 {
    var s: string = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOP";
    var before: i64 = __heap_bump_bytes();
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 1000) {
        total = total + __sum_bytes(s);
        i = i + 1;
    }
    var after: i64 = __heap_bump_bytes();
    write((after - before).to_string());
    write("\n");
    write(total.to_string());
    write("\n");
    return 0;
}
`

func TestX86_64SumBytesAllocatesNothing(t *testing.T) {
	out, exit := compileAndRunX86_64(t, sumBytesNoAllocSrc)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	assertSumBytesNoAlloc(t, out)
}

func TestArm64SumBytesAllocatesNothing(t *testing.T) {
	out, exit := compileAndRunArm64(t, sumBytesNoAllocSrc)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	assertSumBytesNoAlloc(t, out)
}

func assertSumBytesNoAlloc(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two lines (bytes allocated, then the total), got %q", out)
	}
	if got := strings.TrimSpace(lines[0]); got != "0" {
		t.Errorf("1000 __sum_bytes calls moved the bump pointer by %s bytes, want 0", got)
	}
	want := fmt.Sprint(int32(1000 * int64(sumBytesRef("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOP"))))
	if got := strings.TrimSpace(lines[1]); got != want {
		t.Errorf("accumulated total = %s, want %s — the loop did not actually run the kernel", got, want)
	}
}
