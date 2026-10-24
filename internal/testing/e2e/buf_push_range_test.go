package e2e

import (
	"fmt"
	"strings"
	"testing"
)

func runBufPushRangeCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	const alphabet = "abcdefghijklmnopqrst"
	var body strings.Builder
	var want strings.Builder
	for n := 0; n <= len(alphabet); n++ {
		s := alphabet[:n]
		for lo := 0; lo <= n; lo++ {
			for hi := lo; hi <= n; hi++ {
				body.WriteString(fmt.Sprintf("    buf_push_range(b, %s, %d, %d);\n", fernQuote(s), lo, hi))
				want.WriteString(s[lo:hi])
			}
		}
		body.WriteString(fmt.Sprintf("    buf_push(b, %s);\n    buf_push_byte(b, 124);\n", fernQuote(s)))
		want.WriteString(s + "|")
		body.WriteString("    buf_push_u64(b, 4703805599123718721 as u64);\n")
		want.WriteString("ABCDEFGA")
		body.WriteString("    write(buf_take(b)); write(\"\\n\");\n")
		want.WriteString("\n")
	}
	out := run(t, `function main(): i32 {
    let b: usize = buf_new(1);
`+body.String()+`    buf_free(b);
    return 0;
}
`)
	out = strings.TrimRight(out, "\n")
	wantS := strings.TrimRight(want.String(), "\n")
	if out != wantS {
		got := strings.Split(out, "\n")
		exp := strings.Split(wantS, "\n")
		for i := range exp {
			if i >= len(got) || got[i] != exp[i] {
				g := "<missing>"
				if i < len(got) {
					g = got[i]
				}
				t.Fatalf("line %d (strings of %d bytes):\n got %q\nwant %q", i, i, g, exp[i])
			}
		}
		t.Fatalf("output has %d lines, want %d", len(got), len(exp))
	}
}

func TestInterpBufPushRangeCorpus(t *testing.T) {
	runBufPushRangeCorpus(t, func(t *testing.T, src string) string {
		out, exit := runInterpExitCode(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestX86_64BufPushRangeCorpus(t *testing.T) {
	runBufPushRangeCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64BufPushRangeCorpus(t *testing.T) {
	runBufPushRangeCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestWASMBufPushRangeCorpus(t *testing.T) {
	runBufPushRangeCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}
