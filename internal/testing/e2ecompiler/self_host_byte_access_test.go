package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

const byteAccessProg = `@noinline
function table_char(alpha: usize, v: i64): i64 {
    return __load_u8(alpha + (v >> 6 & 63 as i64) as usize) as i64 | __load_u8(alpha + (v & 63 as i64) as usize) as i64 << 8;
}
@noinline
function elem_wide(bs: u8[], i: i32): i64 {
    return bs[i] as i64 << 8 | bs[i + 1] as i64;
}
@noinline
function masked(x: i64): i64 {
    return (x & 255 as i64) as i32 as i64;
}
@noinline
function text_byte(s: string, i: i32): i64 {
    return s[i] as i32 as i64;
}
@noinline
function poke(base: usize, j: i32, v: i32): void {
    __store_u8(base + j as usize, v);
}
function main(): i32 {
    let alpha: usize = __alloc(256);
    let b: i32 = 0;
    while (b < 256) {
        poke(alpha, b, 255 - b);
        b = b + 1;
    }
    let bs: u8[] = [];
    b = 0;
    while (b < 256) {
        bs = bs.append(b as u8);
        b = b + 1;
    }
    b = 0;
    while (b < 255) {
        if (elem_wide(bs, b) != (b * 256 + b + 1) as i64) { return 1; }
        b = b + 1;
    }
    let v: i64 = 0;
    while (v < 4096 as i64) {
        let want: i64 = (255 as i64 - (v >> 6 & 63 as i64)) | (255 as i64 - (v & 63 as i64)) << 8;
        if (table_char(alpha, v) != want) { return 2; }
        v = v + 1 as i64;
    }
    if (masked(0 as i64 - 1 as i64) != 255 as i64) { return 3; }
    if (masked(0 as i64 - 200 as i64) != 56 as i64) { return 4; }
    if (text_byte("aé", 1) != 195 as i64) { return 5; }
    if (text_byte("aé", 2) != 169 as i64) { return 6; }
    return 42;
}
`

func TestSelfHostByteAccessFold(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "byteaccess.fern")
	if err := os.WriteFile(src, []byte(byteAccessProg), 0o644); err != nil {
		t.Fatal(err)
	}
	zeroIndex := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`xorl %\w+, %\w+\n\s+movzbl \(`),
		"arm64-linux":  regexp.MustCompile(`mov x\d+, #0\n\s+ldrb `),
	}
	extended := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movzbl [^\n]*, %(\w+)d\n\s+(movslq %(\w+)d, %(\w+)|movl %(\w+)d, %(\w+)d)\n`),
		"arm64-linux":  regexp.MustCompile(`ldrb w(\d+), [^\n]*\n\s+(sxtw|uxtw|mov w)`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range []string{"table_char", "elem_wide", "text_byte"} {
			body := regexp.MustCompile(`(?s)\n__fn_` + fn + `:\n(.*?)\.cfi_endproc`).FindStringSubmatch(string(raw))
			if body == nil {
				t.Fatalf("%s: __fn_%s not in the listing", target, fn)
			}
			if m := extended[target].FindString(body[1]); m != "" {
				t.Errorf("%s: __fn_%s still extends a byte it loaded:\n%s", target, fn, m)
			}
			if m := zeroIndex[target].FindString(body[1]); m != "" {
				t.Errorf("%s: __fn_%s loads a byte at a computed sum plus a zero index:\n%s", target, fn, m)
			}
		}
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, "byteaccess-"+tg.target)
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		argv := append(append([]string{}, tg.runner...), bin)
		cmd := exec.Command(argv[0], argv[1:]...)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("%s: exit %d, want 42 (1 = a u8 element, 2 = a table byte, 3/4 = a masked byte, 5/6 = a string byte)", tg.target, code)
		}
	}
}
