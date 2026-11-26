package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const loopAlignSrc = `@noinline function sum(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + i * 3; i = i + 1; }
    return t;
}
@noinline function nested(n: i32): i32 {
    let t: i32 = 0;
    for i in 0..n {
        for j in 0..n { t = t + (i ^ j); }
    }
    return t;
}
@noinline function flat(a: i32): i32 {
    if (a > 3) { return a * 2; }
    return a + 1;
}
function main(): i32 {
    return (sum(100) + nested(10) + flat(5)) % 256;
}
`

// The body of the function at label `__fn_<name>:`, up to the next function.
func loopAlignBody(t *testing.T, asm, name string) []string {
	t.Helper()
	var body []string
	in := false
	for _, line := range strings.Split(asm, "\n") {
		if line == "__fn_"+name+":" {
			in = true
		} else if in && strings.HasPrefix(line, "__fn_") && !strings.HasPrefix(line, "__fn_"+name+".") {
			break
		}
		if in {
			body = append(body, line)
		}
	}
	if len(body) == 0 {
		t.Fatalf("no __fn_%s in the assembly", name)
	}
	return body
}

// The labels that `.p2align 6` puts at the start of a 64-byte line.
func loopAlignedLabels(t *testing.T, body []string) []string {
	t.Helper()
	var out []string
	for i, line := range body {
		if strings.TrimSpace(line) != ".p2align 6" {
			continue
		}
		if i+1 >= len(body) || !strings.HasSuffix(body[i+1], ":") {
			t.Fatalf(".p2align 6 is not before a label: %q", body[i+1:])
		}
		out = append(out, strings.TrimSuffix(body[i+1], ":"))
	}
	return out
}

func TestSelfHostX86LoopTopAlignment(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(loopAlignSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := 0
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			nested += i ^ j
		}
	}
	want := (3*4950 + nested + 10) % 256

	asm := readFile(t, cli.emit(t, src, "x86-64-linux"))
	for _, tc := range []struct {
		fn    string
		loops int
	}{{"sum", 1}, {"nested", 2}, {"flat", 0}} {
		body := loopAlignBody(t, asm, tc.fn)
		labels := loopAlignedLabels(t, body)
		if len(labels) != tc.loops {
			t.Errorf("%s: %d aligned labels %v, want %d (one for each loop)", tc.fn, len(labels), labels, tc.loops)
		}
		text := strings.Join(body, "\n")
		for _, l := range labels {
			def := strings.Index(text, "\n"+l+":")
			back := regexp.MustCompile(`(?m)^\s+j[a-z]+ ` + regexp.QuoteMeta(l) + `$`)
			if loc := back.FindStringIndex(text); loc == nil || loc[0] < def {
				t.Errorf("%s: %s is aligned, but no jump after it goes back to it", tc.fn, l)
			}
		}
	}

	small := readFile(t, cli.emit(t, src, "x86-64-linux", "FERN_SIZE=1"))
	for _, fn := range []string{"sum", "nested"} {
		if labels := loopAlignedLabels(t, loopAlignBody(t, small, fn)); len(labels) != 0 {
			t.Errorf("FERN_SIZE=1: %s aligns %v, want no padding", fn, labels)
		}
	}

	if _, code := cli.exitOfFile(t, src, "x86-64-linux", nil); code != want {
		t.Errorf("gas-linked program exited %d, want %d", code, want)
	}
	native := filepath.Join(t.TempDir(), "prog")
	if msg, err := cli.compileCmd("x86-64-linux", native, src).CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target x86-64-linux: %v\n%s", err, msg)
	}
	cmd := runX86_64Bin(cli.runner, native)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("program from the in-process assembler exited %d, want %d", code, want)
	}
}
