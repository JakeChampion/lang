package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

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
	for n := 0; n <= 200; n++ {
		out = append(out, crcCase{0, rnd(n)}, crcCase{0xdeadbeef, rnd(n)})
	}
	for _, n := range []int{15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65,
		111, 112, 113, 127, 128, 129, 175, 176, 177, 191, 192, 193, 255, 256, 257} {
		out = append(out, crcCase{0, rnd(n)}, crcCase{1, rnd(n)}, crcCase{0xffffffff, rnd(n)})
	}
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
		body.WriteString(fmt.Sprintf("    print((__crc32_cksum(%d, %s)).to_string());\n",
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
    let s: string = "The quick brown fox jumps over the lazy dog. ".repeat(23);
    let one: i32 = __crc32_cksum(0, s);
    let acc: i32 = 0;
    let off: i32 = 0;
    let cuts: i32[] = [1, 15, 16, 17, 31, 48, 60, 63, 64, 65, 100, 112, 127, 77];
    let i: i32 = 0;
    while (i < cuts.len()) {
        let take: i32 = cuts[i];
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
