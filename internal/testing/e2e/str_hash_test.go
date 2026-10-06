package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/oracle/interp"
)

// __str_hash(s, seed) is the seeded word-at-a-time string hash
// (compiler/ir.fern's str_hash has the definition; interp.StrHash is the Go
// reference, pinned to an independent implementation by TestStrHashPins).
// The corpus sweeps every length to 40, which crosses the one-word, exact
// multiple and overlapping-tail shapes, from several seeds including a
// negative one, plus NUL and high bytes and random strings.

type strHashCase struct {
	s    string
	seed int
}

func strHashCases() []strHashCase {
	var all strings.Builder
	for c := 0; c < 256; c++ {
		all.WriteByte(byte(c))
	}
	var out []strHashCase
	for n := 0; n <= 40; n++ {
		for _, seed := range []int{0, 1, -1, 208357, -2147483648, 2147483647} {
			out = append(out, strHashCase{all.String()[200 : 200+n], seed})
		}
	}
	out = append(out,
		strHashCase{all.String(), 0},
		strHashCase{all.String(), 77},
		strHashCase{"", 0x12345},
		strHashCase{"abc", 0x12345},
		strHashCase{strings.Repeat("\xff", 300), 0},
		strHashCase{strings.Repeat("\x00", 9), 0},
	)
	rng := rand.New(rand.NewSource(20261006))
	for i := 0; i < 40; i++ {
		n := rng.Intn(120)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte(rng.Intn(256)))
		}
		out = append(out, strHashCase{sb.String(), rng.Intn(1<<32) - 1<<31})
	}
	return out
}

func runStrHashCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := strHashCases()
	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString(fmt.Sprintf("    write((__str_hash(%s, %d)).to_string()); write(\"\\n\");\n", fernQuote(c.s), c.seed))
		want = append(want, fmt.Sprint(int32(interp.StrHash([]byte(c.s), int32(c.seed)))))
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
				t.Errorf("__str_hash(%q, %d) = %s, want %s", cases[i].s, cases[i].seed, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestInterpStrHash(t *testing.T) {
	runStrHashCorpus(t, func(t *testing.T, src string) string {
		out, exit := runInterpExitCode(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestX86_64StrHash(t *testing.T) {
	runStrHashCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64StrHash(t *testing.T) {
	runStrHashCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestWASMStrHash(t *testing.T) {
	runStrHashCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}
