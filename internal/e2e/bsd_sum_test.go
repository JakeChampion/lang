package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// __bsd_sum(s, sum) continues the BSD checksum `sum -r` keeps over s: per
// byte, rotate the 16 bits right by one and add the byte, modulo 2^16. The
// corpus sweeps every length to 40 from several starting sums, inline and
// heap strings, NUL and high bytes, and a starting sum with bits above 16.

func bsdSumRef(s string, sum int) int {
	v := uint32(sum) & 0xffff
	for i := 0; i < len(s); i++ {
		v = (v>>1 | v<<15) & 0xffff
		v = (v + uint32(s[i])) & 0xffff
	}
	return int(v)
}

type bsdSumCase struct {
	s   string
	sum int
}

func bsdSumCases() []bsdSumCase {
	var all strings.Builder
	for c := 0; c < 256; c++ {
		all.WriteByte(byte(c))
	}
	var out []bsdSumCase
	for n := 0; n <= 40; n++ {
		for _, start := range []int{0, 1, 0x8000, 0xffff, 0x1234} {
			out = append(out, bsdSumCase{all.String()[200 : 200+n%56], start})
		}
	}
	out = append(out,
		bsdSumCase{all.String(), 0},
		bsdSumCase{all.String(), 77},
		bsdSumCase{"", 0x12345},
		bsdSumCase{"abc", 0x12345},
		bsdSumCase{strings.Repeat("\xff", 300), 0},
	)
	rng := rand.New(rand.NewSource(20260923))
	for i := 0; i < 40; i++ {
		n := rng.Intn(120)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte(rng.Intn(256)))
		}
		out = append(out, bsdSumCase{sb.String(), rng.Intn(65536)})
	}
	return out
}

func runBsdSumCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := bsdSumCases()
	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((__bsd_sum(%s, %d)).to_string()); write(\"\\n\");\n", fernQuote(c.s), c.sum))
		want = append(want, fmt.Sprint(bsdSumRef(c.s, c.sum)))
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
				t.Errorf("__bsd_sum(%q, %d) = %s, want %s", cases[i].s, cases[i].sum, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestInterpBsdSum(t *testing.T) {
	runBsdSumCorpus(t, func(t *testing.T, src string) string {
		out, exit := runInterpExitCode(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestX86_64BsdSum(t *testing.T) {
	runBsdSumCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64BsdSum(t *testing.T) {
	runBsdSumCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestWASMBsdSum(t *testing.T) {
	runBsdSumCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}

func TestX86_64SSABsdSum(t *testing.T) {
	runBsdSumCorpus(t, x86_64SSACorpusRunner(t))
}

func TestArm64SSABsdSum(t *testing.T) {
	runBsdSumCorpus(t, arm64SSACorpusRunner(t))
}
