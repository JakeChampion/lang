package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// On x86-64 a function with no frame slots that makes no call builds no %rbp
// frame: it pushes only the callee-saved registers it uses and pops them
// before each return. mix loops, early returns before its loop, and wide keeps
// enough values live to need callee-saved registers; outer calls, so it keeps
// its frame.
const leafFrameProg = `@noinline function mix(s: string, n: i32): i32 {
    if (n <= 1) { return 0; }
    let a: i32 = 208357;
    let i: i32 = 0;
    while (i < s.len()) {
        a = a * 31 + s[i] as i32;
        i = i + 1;
    }
    return (a & 1073741823) %% n;
}

@noinline function wide(a: i64, b: i64, c: i64, d: i64): i64 {
    let e: i64 = a * b;
    let f: i64 = b * c;
    let g: i64 = c * d;
    let h: i64 = d * a;
    let p: i64 = a + c;
    return e + f + g + h + p + a * d + b * c;
}

@noinline function outer(s: string): i32 {
    return mix(s, 97) + mix(s + "!", 89);
}

function main(): i32 {
    if (mix("leaf", 1) != 0) { return 1; }
    if (mix("leaf-frame", 1009) != %d) { return 2; }
    if (wide(3i64, -5i64, 7i64, 11i64) != %dI64) { return 3; }
    if (outer("abc") != %d) { return 4; }
    return 42;
}
`

func leafMix(s string, n int32) int32 {
	if n <= 1 {
		return 0
	}
	a := int32(208357)
	for i := 0; i < len(s); i++ {
		a = a*31 + int32(s[i])
	}
	return (a & 1073741823) % n
}

func leafWide(a, b, c, d int64) int64 {
	e, f, g, h := a*b, b*c, c*d, d*a
	p := a + c
	return e + f + g + h + p + a*d + b*c
}

// leafFunction is the listing of fn's register entry through its .cfi_endproc.
func leafFunction(t *testing.T, asm, fn string) string {
	t.Helper()
	at := strings.Index(asm, "__fn_"+fn+".r:\n")
	if at < 0 {
		t.Fatalf("no register entry for %s in the listing", fn)
	}
	body := asm[at:]
	return body[:strings.Index(body, ".cfi_endproc")]
}

func TestSelfHostLeafFrame(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	prog := fmt.Sprintf(leafFrameProg, leafMix("leaf-frame", 1009), leafWide(3, -5, 7, 11), leafMix("abc", 97)+leafMix("abc!", 89))
	prog = strings.ReplaceAll(prog, "I64", "i64")
	src := filepath.Join(dir, "leaf.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "leaf.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	asm := string(raw)
	for _, fn := range []string{"mix", "wide"} {
		body := leafFunction(t, asm, fn)
		if strings.Contains(body, "%rbp") || strings.Contains(body, "leave") {
			t.Errorf("leaf %s builds a frame:\n%s", fn, body)
		}
		if strings.Count(body, "    ret\n") < 1 {
			t.Errorf("leaf %s never returns:\n%s", fn, body)
		}
	}
	if wide := leafFunction(t, asm, "wide"); !strings.Contains(wide, "pushq %r") || !strings.Contains(wide, ".cfi_def_cfa_offset 16") {
		t.Errorf("wide saves no callee-saved register against %%rsp:\n%s", wide)
	}
	if !strings.Contains(leafFunction(t, asm, "outer"), "pushq %rbp") {
		t.Errorf("outer calls, but builds no frame")
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
		if combined, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
