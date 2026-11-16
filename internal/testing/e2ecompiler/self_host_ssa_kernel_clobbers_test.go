package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const ssaKernelClobbersProg = `function scan(s: string, a: i32, b: i32, c: i32): i32 {
    let i: i32 = 0;
    let runs: i32 = 0;
    let acc: i32 = 0;
    while (i < s.len()) {
        let j: i32 = __ascii_run(s, i);
        runs = runs + 1;
        acc = acc + (j - i) * a + b * runs - c;
        i = j + 1;
    }
    return acc + runs;
}
function fields(s: string, a: i32, b: i32, c: i32): i32 {
    let i: i32 = 0;
    let runs: i32 = 0;
    let acc: i32 = 0;
    while (i < s.len()) {
        let j: i32 = __memchr(s, 44, i);
        if (j < 0) { return acc + runs; }
        runs = runs + 1;
        acc = acc + (j - i) * a + b * runs - c + __rmemchr(s, 44, j);
        i = j + 1;
    }
    return acc + runs;
}
function sums(s: string, a: i32, b: i32, c: i32): i32 {
    let i: i32 = 0;
    let runs: i32 = 0;
    let acc: i32 = 0;
    while (i < 6) {
        let h: i32 = __str_hash(s, i) & 1023;
        let n: i32 = __count_byte(s, 97 + i);
        let m: i32 = __bsd_sum(s, i) & 255;
        runs = runs + 1;
        acc = acc + h * a + n * b * runs - c + m;
        i = i + 1;
    }
    return acc + runs;
}
function sparse(s: string, a: i32, b: i32, c: i32): i32 {
    let i: i32 = 0;
    let runs: i32 = 0;
    let acc: i32 = 0;
    while (i < s.len()) {
        if ((s[i] as i32) < 128) {
            let j: i32 = __ascii_run(s, i);
            acc = acc + (j - i) * a;
            i = j;
        } else {
            runs = runs + 1;
            acc = acc + b * runs - c;
            i = i + 1;
        }
    }
    return acc + runs;
}
function main(): i32 {
    let hi: string = unsafe { string_from_bytes_unchecked([195 as u8]) };
    let s: string = "hello" + hi + "world, a longer run of ascii bytes, that passes one vector block" + hi + "x";
    let t: string = hi + hi + "ab" + hi;
    if (scan(t, 2, 3, 5) != 10) { return 1; }
    if (scan(s, 3, 5, 7) != 222) { return 2; }
    let f: i32 = fields(s, 3, 5, 7) + fields(t, 1, 2, 3);
    let g: i32 = sums(s, 3, 5, 7) + sums(t, 1, 2, 3);
    if (sparse(t, 2, 3, 5) != 10) { return 5; }
    if (sparse(s, 3, 5, 7) != 213) { return 6; }
    if (f != 171) { return 3; }
    if (g != 12572) { return 4; }
    return ((f + g) & 63) + 64;
}
`

var ssaKernelLine = regexp.MustCompile(`(?m)^\s+\d+: v\d+ (\w+) cold=(\d+)$`)

// An inline byte kernel of the x86-64 emitter changes only the registers
// that its text names. A value that lives across the kernel keeps one of the
// other caller registers. scan has seven values that live across
// __ascii_run, and five saved registers: three of the values had a frame
// slot, and each pass of the loop read them from the frame.
func TestSelfHostSSAKernelClobbers(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(ssaKernelClobbersProg), 0o644); err != nil {
		t.Fatal(err)
	}
	kernels := map[string][]string{
		"scan":   {"ascii_run"},
		"sparse": {"ascii_run"},
		"fields": {"memchr", "rmemchr"},
		"sums":   {"str_hash", "count_byte", "bsd_sum"},
	}
	for fn, want := range kernels {
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "out"), src, cli.stdlib)
		cmd.Env = append(os.Environ(), "FERN_SSA_ALLOC="+fn)
		msg, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", fn, err, msg)
		}
		table := string(msg)
		var got []string
		for _, m := range ssaKernelLine.FindAllStringSubmatch(table, -1) {
			if m[2] != "5" {
				t.Errorf("%s: %s has cold=%s, want the kernel kind 5", fn, m[1], m[2])
			}
			got = append(got, m[1])
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s: kernels %v, want %v\n%s", fn, got, want, table)
		}
		if fn == "scan" && strings.Contains(table, " slot ") {
			t.Errorf("scan has a value in a frame slot:\n%s", table)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, ssaKernelClobbersProg, target); code != 71 {
			t.Errorf("%s: exit %d, want 71\n%s", target, code, stderr)
		}
	}
}
