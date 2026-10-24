package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

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

	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		out = append(out, c{base, 0})
		for at := 0; at < n; at++ {
			hi := base[:at] + "\xc3" + base[at+1:]
			out = append(out, c{hi, 0})
			out = append(out, c{hi, at})
			out = append(out, c{hi, at + 1})
		}
	}

	for _, b := range []byte{0x80, 0xBF, 0xC0, 0xE0, 0xF0, 0xF5, 0xFE, 0xFF} {
		out = append(out, c{"abc" + string([]byte{b}) + "def", 0})
		out = append(out, c{strings.Repeat("x", 20) + string([]byte{b}), 0})
	}

	out = append(out,
		c{"\x7f\x7f\x7f", 0},
		c{strings.Repeat("\x7f", 20), 0},
		c{"", 0},
		c{"abc", 100},
		c{"abc", -5},
		c{"\xffabc", 0},
		c{strings.Repeat("a", 300) + "\xc3", 300},
	)

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

func TestArm64AsciiRun(t *testing.T) {
	runAsciiRunCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestWASMAsciiRun(t *testing.T) {
	runAsciiRunCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}
