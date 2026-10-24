package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

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

	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		out = append(out, c{base, 'z', n})
		for at := 0; at < n; at++ {
			withHit := base[:at] + "z" + base[at+1:]
			out = append(out, c{withHit, 'z', n})
			out = append(out, c{withHit, 'z', at})
			out = append(out, c{withHit, 'z', at - 1})
			out = append(out, c{withHit, 'a', at})
		}
	}

	out = append(out,
		c{"", 'a', 0},
		c{"abc", 'a', 100},
		c{"abc", 'c', -1},
		c{"abc", 'a', -5},
		c{"abc", 256, 2},
		c{"abc", -1, 2},
		c{"aaa", 'a', 2},
		c{"aba", 'a', 2},
		c{"aba", 'a', 1},
		c{"\x00b\x00", 0, 2},
		c{"\x00b\x00", 0, 1},
		c{"\xff\xfe", 0xff, 1},
		c{"\xff\xfe", 0xfe, 1},
		c{strings.Repeat("q", 300), 'q', 0},
	)

	for _, n := range []int{31, 32, 33, 47, 48, 49, 63, 64, 65, 79, 80, 81} {
		base := strings.Repeat("a", n)
		out = append(out, c{base, 'z', n})
		for at := 0; at < n; at++ {
			withHit := base[:at] + "z" + base[at+1:]
			out = append(out, c{withHit, 'z', n})
			out = append(out, c{withHit, 'z', at})
			out = append(out, c{withHit, 'z', at - 1})
			out = append(out, c{withHit, 'a', at})
		}
	}

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
