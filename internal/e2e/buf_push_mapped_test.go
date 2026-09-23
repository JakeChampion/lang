package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// buf_push_mapped(b, s, table) appends table[c] for each byte c of s, or c
// itself when it is at or past the table's end. The corpus sweeps lengths
// across the unrolled loop's boundaries, a full table, a short one, an empty
// one, NUL and high bytes, and pushes that land after bytes already held and
// that outgrow the builder.

func bufPushMappedRef(held string, s string, table []byte) string {
	out := []byte(held)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if int(c) < len(table) {
			c = table[c]
		}
		out = append(out, c)
	}
	return string(out)
}

type bufMapCase struct {
	held  string
	s     string
	table []byte
}

func bufMapCases() []bufMapCase {
	rot := make([]byte, 256)
	for i := range rot {
		rot[i] = byte(i + 13)
	}
	digits := make([]byte, 256)
	for i := range digits {
		digits[i] = byte(i)
	}
	for c := '0'; c <= '9'; c++ {
		digits[c] = byte('a' + c - '0')
	}
	short := []byte{'x', 'y', 'z', 0xff}

	var out []bufMapCase
	var all strings.Builder
	for c := 0; c < 256; c++ {
		all.WriteByte(byte(c))
	}
	for n := 0; n <= 40; n++ {
		s := all.String()[200 : 200+n%56]
		if n > 55 {
			s = all.String()[:n]
		}
		out = append(out, bufMapCase{"", s, rot})
		out = append(out, bufMapCase{"", all.String()[:n], short})
		out = append(out, bufMapCase{"ab", strings.Repeat("0123456789", 4)[:n], digits})
	}
	out = append(out,
		bufMapCase{"", all.String(), rot},
		bufMapCase{"", all.String(), short},
		bufMapCase{"", all.String(), []byte{}},
		bufMapCase{"held", "\x00\x01\x02\x03", short},
		bufMapCase{"", "\xff\xfe\x80\x00", rot},
		bufMapCase{"", strings.Repeat("9876543210", 30), digits},
		bufMapCase{strings.Repeat("h", 70), strings.Repeat("q7", 90), digits},
	)
	rng := rand.New(rand.NewSource(20260923))
	for i := 0; i < 40; i++ {
		n := rng.Intn(90)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte(rng.Intn(256)))
		}
		table := rot
		if i%3 == 1 {
			table = short
		}
		out = append(out, bufMapCase{strings.Repeat("p", rng.Intn(9)), sb.String(), table})
	}
	return out
}

// runBufPushMappedCorpus pushes every case through a builder that starts at
// four bytes of capacity, so most pushes grow it, and prints each result as
// its byte values.
func runBufPushMappedCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := bufMapCases()
	tables := map[string]string{}
	var decls, body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		key := string(c.table)
		name, ok := tables[key]
		if !ok {
			name = fmt.Sprintf("tab%d", len(tables))
			tables[key] = name
			decls.WriteString(fmt.Sprintf("    var %s: u8[] = %s;\n", name, fernSet(c.table)))
		}
		if c.held != "" {
			body.WriteString(fmt.Sprintf("    buf_push(b, %s);\n", fernQuote(c.held)))
		}
		body.WriteString(fmt.Sprintf("    buf_push_mapped(b, %s, %s);\n    dump(buf_take(b));\n",
			fernQuote(c.s), name))
		ref := bufPushMappedRef(c.held, c.s, c.table)
		var line strings.Builder
		for i := 0; i < len(ref); i++ {
			fmt.Fprintf(&line, "%d,", ref[i])
		}
		want = append(want, line.String()+".")
	}

	out := run(t, `import "std/i32";

function dump(s: string): void {
    var line: string = "";
    var i: i32 = 0;
    while (i < s.len()) {
        line = line + (s[i] as i32).to_string() + ",";
        i = i + 1;
    }
    write(line + ".\n");
}

function main(): i32 {
    var b: usize = buf_new(4);
`+decls.String()+body.String()+`    buf_free(b);
    return 0;
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
				t.Errorf("held %q, buf_push_mapped(%q, %v):\n got %s\nwant %s",
					cases[i].held, cases[i].s, cases[i].table, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestInterpBufPushMapped(t *testing.T) {
	runBufPushMappedCorpus(t, func(t *testing.T, src string) string {
		out, exit := runInterpExitCode(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestX86_64BufPushMapped(t *testing.T) {
	runBufPushMappedCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64BufPushMapped(t *testing.T) {
	runBufPushMappedCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestWASMBufPushMapped(t *testing.T) {
	runBufPushMappedCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}

func TestX86_64SSABufPushMapped(t *testing.T) {
	runBufPushMappedCorpus(t, x86_64SSACorpusRunner(t))
}

func TestArm64SSABufPushMapped(t *testing.T) {
	runBufPushMappedCorpus(t, arm64SSACorpusRunner(t))
}
