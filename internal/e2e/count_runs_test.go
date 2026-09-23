package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// __count_runs(s, inside, set) counts the runs of bytes whose entry in the u8[]
// `set` is nonzero that begin in s; `inside` nonzero says the byte before s was
// a member, so a run open at s[0] is not counted. A byte past the end of `set`
// is not a member. The corpus sweeps both values of `inside`, every length up
// to 40 across the unrolled loop's pairs, a full set, a short one, an empty
// one, NUL and high bytes, and random strings over a small alphabet.

func countRunsRef(s string, inside int, set []byte) int {
	prev := inside != 0
	runs := 0
	for i := 0; i < len(s); i++ {
		c := int(s[i])
		member := c < len(set) && set[c] != 0
		if member && !prev {
			runs++
		}
		prev = member
	}
	return runs
}

type countRunsCase struct {
	s      string
	inside int
	set    []byte
}

func countRunsCases() []countRunsCase {
	space := fullSet(' ', '\t', '\n', '\v', '\f', '\r')
	high := fullSet(0xff, 0x80, 0)
	short := []byte{0, 1, 0, 1}
	var out []countRunsCase
	for n := 0; n <= 40; n++ {
		var sb strings.Builder
		for j := 0; j < n; j++ {
			if j%3 == 2 || j%7 == 0 {
				sb.WriteByte(' ')
			} else {
				sb.WriteByte('a' + byte(j%26))
			}
		}
		for inside := 0; inside <= 1; inside++ {
			out = append(out, countRunsCase{sb.String(), inside, space})
			out = append(out, countRunsCase{strings.Repeat(" ", n), inside, space})
			out = append(out, countRunsCase{strings.Repeat("x", n), inside, space})
		}
	}
	out = append(out,
		countRunsCase{"", 0, space},
		countRunsCase{"", 1, space},
		countRunsCase{"\x01\x03\x01\x02\x01", 0, short},
		countRunsCase{"\x01\x03\x01\x02\x01", 1, short},
		countRunsCase{"\x00\x02\x00", 0, short},
		countRunsCase{"abc", 0, []byte{}},
		countRunsCase{"\xff\xfe\x80\x00x\x00", 0, high},
		countRunsCase{"\xff\xfe\x80\x00x\x00", 1, high},
		countRunsCase{"the quick\tbrown\n\nfox  jumps", 0, space},
		countRunsCase{"the quick\tbrown\n\nfox  jumps", 1, space},
		countRunsCase{strings.Repeat("ab ", 200), 0, space},
	)
	rng := rand.New(rand.NewSource(20260923))
	dense := fullSet('a', 'c')
	for i := 0; i < 60; i++ {
		n := rng.Intn(90)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte('a' + rng.Intn(4)))
		}
		out = append(out, countRunsCase{sb.String(), rng.Intn(2), dense})
	}
	return out
}

func runCountRunsCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := countRunsCases()
	sets := map[string]string{}
	var decls, body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		key := string(c.set)
		name, ok := sets[key]
		if !ok {
			name = fmt.Sprintf("set%d", len(sets))
			sets[key] = name
			decls.WriteString(fmt.Sprintf("    var %s: u8[] = %s;\n", name, fernSet(c.set)))
		}
		body.WriteString(fmt.Sprintf("    write((__count_runs(%s, %d, %s)).to_string()); write(\"\\n\");\n",
			fernQuote(c.s), c.inside, name))
		want = append(want, fmt.Sprint(countRunsRef(c.s, c.inside, c.set)))
	}
	out := run(t, `import "std/i32";

function main(): i32 {
`+decls.String()+body.String()+`    return 0;
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
				t.Errorf("__count_runs(%q, %d, %v) = %s, want %s",
					cases[i].s, cases[i].inside, cases[i].set, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestInterpCountRuns(t *testing.T) {
	runCountRunsCorpus(t, func(t *testing.T, src string) string {
		out, exit := runInterpExitCode(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestX86_64CountRuns(t *testing.T) {
	runCountRunsCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64CountRuns(t *testing.T) {
	runCountRunsCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestWASMCountRuns(t *testing.T) {
	runCountRunsCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}

func TestX86_64SSACountRuns(t *testing.T) {
	runCountRunsCorpus(t, x86_64SSACorpusRunner(t))
}

func TestArm64SSACountRuns(t *testing.T) {
	runCountRunsCorpus(t, arm64SSACorpusRunner(t))
}
