package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// __crc32_cksum is the seventh kernel of docs/ATLAS-PLATFORM-PLAN.md §3, and
// the first CARRIED one: the state goes in and comes back, so a chunked
// stream and a one-shot call have to agree.
//
// The compiled backends fold sixteen bytes a step with a carry-less multiply
// against a scalar interpreter oracle, and the folds get three things wrong
// that a byte loop cannot:
//
//   - BYTE ORDER. The message is a polynomial with byte 0 most significant,
//     the opposite of the order a 16-byte load produces, so each block is
//     reversed before folding. Reversing wrongly still yields a CRC — of a
//     different message — so only a full length sweep separates them.
//   - the BLOCK BOUNDARIES. Four accumulators 64 bytes apart prime off three
//     extra blocks, so 16, 64, 112 and 128 bytes are each a different path
//     into or out of the wide loop, and the residue between them is what a
//     mis-stepped pointer drops.
//   - the CARRIED STATE. Every other kernel starts from nothing each call.
//     This one does not, so a fold that absorbs the incoming CRC at the wrong
//     bit position agrees on a zero CRC and on nothing else.
func crc32CksumRef(crc uint32, s string) int32 {
	for i := 0; i < len(s); i++ {
		crc ^= uint32(s[i]) << 24
		for k := 0; k < 8; k++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return int32(crc)
}

type crcCase struct {
	crc uint32
	s   string
}

func crc32CksumCases() []crcCase {
	var out []crcCase
	r := rand.New(rand.NewSource(7))
	rnd := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(r.Intn(256))
		}
		return string(b)
	}
	// Exhaustive over length through the 4-way prime (48) and its first full
	// 64-byte step, so every entry and exit path is hit at a zero and at a
	// non-zero incoming CRC.
	for n := 0; n <= 200; n++ {
		out = append(out, crcCase{0, rnd(n)}, crcCase{0xdeadbeef, rnd(n)})
	}
	// The exact boundaries, where an off-by-one block is a real instruction
	// path rather than a rounding of the same one.
	for _, n := range []int{15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65,
		111, 112, 113, 127, 128, 129, 175, 176, 177, 191, 192, 193, 255, 256, 257} {
		out = append(out, crcCase{0, rnd(n)}, crcCase{1, rnd(n)}, crcCase{0xffffffff, rnd(n)})
	}
	// High bytes only: a sign-extending load or a signed shift in the step
	// passes everything ASCII and fails here.
	for _, n := range []int{16, 64, 128, 200} {
		out = append(out, crcCase{0x80000000, strings.Repeat("\xff", n)})
	}
	return out
}

func runCrc32CksumCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := crc32CksumCases()
	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((__crc32_cksum(%d, %s)).to_string()); write(\"\\n\");\n",
			int32(c.crc), fernQuote(c.s)))
		want = append(want, fmt.Sprint(crc32CksumRef(c.crc, c.s)))
	}
	out := run(t, "import \"std/i32\";\n\nfunction main(): i32 {\n"+body.String()+"    return 0;\n}\n")
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("printed %d lines, want %d — the program and the expectation list are out of step",
			len(got), len(want))
	}
	bad := 0
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("__crc32_cksum(%d, %d bytes) = %s, want %s",
					int32(cases[i].crc), len(cases[i].s), strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestX86_64Crc32Cksum(t *testing.T) {
	runCrc32CksumCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64Crc32Cksum(t *testing.T) {
	runCrc32CksumCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

// The wasm leg runs the same corpus against a bit-at-a-time body rather than a
// fold — wasm has no carry-less multiply — so this is where the corpus proves
// the DEFINITION and the native kernels agree, not just that two folds agree
// with each other. It also adds a case class the native legs lack: a short
// string lives in its two words with no address, and is read through
// __fern_str_byte.
func TestWASMCrc32Cksum(t *testing.T) {
	runCrc32CksumCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}

// The streaming identity the carried state exists for: the same bytes cut at
// irregular offsets must fold to the same CRC as one call. Cuts straddle the
// single-block, prime and 4-way boundaries, and none of them divides 64.
// The arm64 kernel gets the same streaming check, and it carries a second
// job there: the loop keeps several locals live ACROSS the call, which is
// what a kernel clobbering a callee-saved register corrupts. The corpus
// cannot see that — its call sites are straight-line expressions with
// nothing live over them — so a kernel that destroyed x19/x20 passed 487
// cases and still miscompiled its caller.
func TestArm64Crc32CksumStreams(t *testing.T) {
	runCrc32CksumStreams(t, func(t *testing.T, src string) (string, int) {
		return compileAndRunArm64(t, src)
	})
}

func TestX86_64Crc32CksumStreams(t *testing.T) {
	runCrc32CksumStreams(t, func(t *testing.T, src string) (string, int) {
		return compileAndRunX86_64(t, src)
	})
}

func runCrc32CksumStreams(t *testing.T, run func(t *testing.T, src string) (string, int)) {
	t.Helper()
	src := `import "std/i32";

function main(): i32 {
    var s: string = "The quick brown fox jumps over the lazy dog. ".repeat(23);
    var one: i32 = __crc32_cksum(0, s);
    var acc: i32 = 0;
    var off: i32 = 0;
    var cuts: i32[] = [1, 15, 16, 17, 31, 48, 60, 63, 64, 65, 100, 112, 127, 77];
    var i: i32 = 0;
    while (i < cuts.len()) {
        var take: i32 = cuts[i];
        if (off + take > s.len()) { take = s.len() - off; }
        if (take > 0) { acc = __crc32_cksum(acc, slice_unchecked(s, off, off + take) + ""); }
        off = off + take;
        i = i + 1;
    }
    if (off < s.len()) { acc = __crc32_cksum(acc, slice_unchecked(s, off, s.len()) + ""); }
    write(one.to_string()); write("\n");
    write(acc.to_string()); write("\n");
    return 0;
}
`
	out, exit := run(t, src)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), out)
	}
	if strings.TrimSpace(lines[0]) != strings.TrimSpace(lines[1]) {
		t.Errorf("chunked %s, one-shot %s — the carried state does not survive a chunk boundary",
			strings.TrimSpace(lines[1]), strings.TrimSpace(lines[0]))
	}
}
