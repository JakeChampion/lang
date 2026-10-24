package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

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

	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		out = append(out, c{base, 'z'})
		out = append(out, c{base, 'a'})
		for at := 0; at < n; at++ {
			withHit := base[:at] + "z" + base[at+1:]
			out = append(out, c{withHit, 'z'})
			out = append(out, c{withHit, 'a'})
		}
	}

	for _, n := range []int{15, 16, 17, 31, 32, 33, 47, 48, 64, 300} {
		var ab, aab strings.Builder
		for i := 0; i < n; i++ {
			ab.WriteByte("ab"[i%2])
			aab.WriteByte("aab"[i%3])
		}
		out = append(out, c{ab.String(), 'a'}, c{ab.String(), 'b'})
		out = append(out, c{aab.String(), 'a'}, c{aab.String(), 'b'})
	}

	for _, n := range []int{31, 32, 33, 47, 48, 49, 63, 64, 65, 79, 80, 81} {
		base := strings.Repeat("a", n)
		out = append(out, c{base, 'z'})
		out = append(out, c{base, 'a'})
		for at := 0; at < n; at++ {
			withHit := base[:at] + "z" + base[at+1:]
			out = append(out, c{withHit, 'z'})
			out = append(out, c{withHit, 'a'})
		}
	}

	out = append(out,
		c{"", 'a'},
		c{"", 0},
		c{"abc", 256},
		c{"abc", -1},
		c{"aaa", 256},
		c{"\x00b\x00", 0},
		c{"\xff\xfe\xff", 0xff},
		c{"\xff\xfe\xff", 0xfe},
		c{"\xff\xfe\xff", 0x7f},
		c{strings.Repeat("q", 300), 'q'},
		c{strings.Repeat("q", 300), 'z'},
		c{strings.Repeat("q", 255) + "z", 'z'},
	)

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
