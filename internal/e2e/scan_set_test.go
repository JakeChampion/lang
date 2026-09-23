package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// __scan_set(s, from, set) is the byte-set scan: the index of the first byte
// of `s` at or after `from` whose entry in `set`, a u8[] indexed by byte
// value, is nonzero, or len(s) when no byte qualifies. A byte past the end of
// `set` is not in the set. It is what wc's word split, cat -A's spelling and
// tr -d walk the input with, so its corpus sweeps the shapes those callers
// produce: dense and sparse sets, a short set, the cursor at every offset,
// and NUL and high bytes on both sides.

// scanSetRef is the reference semantics, matching the interpreter builtin.
func scanSetRef(s string, from int, set []byte) int {
	if from < 0 {
		from = 0
	}
	for i := from; i < len(s); i++ {
		b := int(s[i])
		if b < len(set) && set[b] != 0 {
			return i
		}
	}
	return len(s)
}

type scanSetCase struct {
	s    string
	from int
	set  []byte
}

func fullSet(members ...byte) []byte {
	set := make([]byte, 256)
	for _, m := range members {
		set[m] = 1
	}
	return set
}

func scanSetCases() []scanSetCase {
	var out []scanSetCase
	space := fullSet(' ', '\t', '\n', '\v', '\f', '\r')
	nul := fullSet(0)
	high := fullSet(0xff, 0x80)

	// The cursor at every offset over a haystack with one whitespace byte at
	// each position, across the sizes a caller's run walk produces.
	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		for from := -1; from <= n+1; from++ {
			out = append(out, scanSetCase{base, from, space})
		}
		for at := 0; at < n; at++ {
			withHit := base[:at] + " " + base[at+1:]
			out = append(out, scanSetCase{withHit, 0, space})
			out = append(out, scanSetCase{withHit, at, space})
			out = append(out, scanSetCase{withHit, at + 1, space})
		}
	}

	// A short set: bytes past its end are never members, whatever the
	// haystack holds.
	short := []byte{0, 1, 0, 1}
	out = append(out,
		scanSetCase{"abc", 0, short},
		scanSetCase{"\x01\x03\x02", 0, short},
		scanSetCase{"\x00\x02\x01", 0, short},
		scanSetCase{"\x03\x03\x03", 0, short},
		scanSetCase{"", 0, short},
		scanSetCase{"abc", 0, []byte{}},
	)

	// Edge cases the sweep cannot express.
	out = append(out,
		scanSetCase{"", 0, space},
		scanSetCase{"", -5, space},
		scanSetCase{"\x00b\x00", 0, nul},
		scanSetCase{"\x00b\x00", 1, nul},
		scanSetCase{"\xff\xfe\x80", 0, high},
		scanSetCase{"\x7f\xfe\x80", 0, high},
		scanSetCase{"\x7f\xfe\x80", 3, high},
		scanSetCase{strings.Repeat("q", 300), 0, space},
		scanSetCase{strings.Repeat("q", 300) + "\n", 0, space},
		scanSetCase{strings.Repeat("q", 300) + "\n", 300, space},
		scanSetCase{strings.Repeat("q", 300) + "\n", 301, space},
		scanSetCase{"the quick\tbrown\nfox", 0, space},
		scanSetCase{"the quick\tbrown\nfox", 4, space},
		scanSetCase{"the quick\tbrown\nfox", 10, space},
	)

	// Deterministic randoms over a small alphabet with a dense set, so hits
	// land at irregular offsets and the cursor starts anywhere.
	rng := rand.New(rand.NewSource(20260922))
	dense := fullSet('a', 'c')
	for i := 0; i < 60; i++ {
		n := rng.Intn(70)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte('a' + rng.Intn(4)))
		}
		out = append(out, scanSetCase{sb.String(), rng.Intn(n + 2), dense})
	}
	return out
}

// fernSet spells a u8[] literal for a set.
func fernSet(set []byte) string {
	if len(set) == 0 {
		return "[]"
	}
	parts := make([]string, len(set))
	for i, b := range set {
		parts[i] = fmt.Sprintf("%d as u8", b)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// runScanSetCorpus drives the corpus through one backend and compares every
// answer against scanSetRef. The sets are built once each, since a 256-entry
// literal per case would make the program the size of the corpus.
func runScanSetCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := scanSetCases()

	var body strings.Builder
	sets := map[string]string{}
	var decls strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		key := string(c.set)
		name, ok := sets[key]
		if !ok {
			name = fmt.Sprintf("set%d", len(sets))
			sets[key] = name
			decls.WriteString(fmt.Sprintf("    var %s: u8[] = %s;\n", name, fernSet(c.set)))
		}
		body.WriteString(fmt.Sprintf("    write((__scan_set(%s, %d, %s)).to_string()); write(\"\\n\");\n",
			fernQuote(c.s), c.from, name))
		want = append(want, fmt.Sprint(scanSetRef(c.s, c.from, c.set)))
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
				t.Errorf("__scan_set(%q, %d, %v) = %s, want %s",
					cases[i].s, cases[i].from, cases[i].set, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestInterpScanSet(t *testing.T) {
	runScanSetCorpus(t, func(t *testing.T, src string) string {
		out, exit := runInterpExitCode(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestX86_64ScanSet(t *testing.T) {
	runScanSetCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64ScanSet(t *testing.T) {
	runScanSetCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

// The wasm leg's short haystacks live in their two words with no address, so
// the kernel reads them through __fern_str_byte.
func TestWASMScanSet(t *testing.T) {
	runScanSetCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}

func TestArm64SSAScanSet(t *testing.T) {
	runScanSetCorpus(t, arm64SSACorpusRunner(t))
}
