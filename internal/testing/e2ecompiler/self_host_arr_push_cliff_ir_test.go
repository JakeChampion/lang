package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

var arrPushCliffIRCases = []struct {
	name string
	main string
	want int
}{
	{"healthy-threaded-accumulator", `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`, 0},
	{"shared-buffer-with-spare-capacity", `function main(): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    let b: i32[] = a;
    let c: i32[] = a.append(99);
    if (b.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 99 || b[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`, 1},
	{"shared-param-accumulator-appended-in-a-loop", `function chunk(out: i32[], s: i32[]): i32[] {
    let bs: i32[] = out;
    let i: i32 = 0;
    while (i < s.len()) { bs = bs.append(s[i]); i = i + 1; }
    return bs;
}
function main(): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    let s: i32[] = [];
    s = s.append(9);
    let keep: i32[] = a;
    let c: i32[] = chunk(a, s);
    if (keep.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 9 || keep[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`, 1},
}

func TestSelfHostArrPushCliffIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrPushCliffIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}

const arrPushRecycledProg = `function fill(n: i32): i64[] {
    let out: i64[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append(0 as i64); i = i + 1; }
    return out;
}
function churn(k: i32): i32 {
    let total: i32 = 0;
    let j: i32 = 0;
    while (j < k) {
        let b: u8[] = __alloc_u8(262152);
        b = b.with(0, 255 as u8);
        let s: string = string_from_bytes_unchecked(b);
        total = total + s.len() % 3;
        j = j + 1;
    }
    return total;
}
function main(): i32 {
    let before: i32 = __arr_push_shared_count();
    let c: i32 = churn(8);
    let xs: i64[] = fill(40000);
    if (xs.len() != 40000 || c < 0) { return 250; }
    if (__arr_push_shared_count() != before) { return 1; }
    return 0;
}
`

func TestSelfHostArrPushRecycledBlockIsNotShared(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, arrPushRecycledProg, target); code != 0 {
				t.Errorf("%s exited %d, want 0: a recycled block's receiver is taken as shared\n%s", target, code, stderr)
			}
		})
	}
}

func TestSelfHostArrPushRecycledBlockIsNotSharedOnHost(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "recycled.fern")
	if err := os.WriteFile(src, []byte(arrPushRecycledProg), 0o644); err != nil {
		t.Fatal(err)
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
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0: a recycled block's receiver is taken as shared", tg.target, got)
		}
	}
}

func TestSelfHostArrPushReadsCountAsWord(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "push.fern")
	if err := os.WriteFile(src, []byte(arrPushRecycledProg), 0o644); err != nil {
		t.Fatal(err)
	}
	wide := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movq -8\(%r[a-z0-9]+\), %r[a-z0-9]+\n\s+cmpq \$1`),
		"arm64-linux":  regexp.MustCompile(`ldur x[0-9]+, \[x[0-9]+, #-8\]\n\s+cmp x[0-9]+, #1`),
	}
	narrow := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movl -8\(%rdi\), %ecx\n\s+cmpl \$1, %ecx`),
		"arm64-linux":  regexp.MustCompile(`ldur w3, \[x0, #-8\]\n\s+cmp w3, #1`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		cmd := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib)
		if combined, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		head := arrPushHead(t, string(asm), "__fern_arr_push")
		if !narrow[target].MatchString(head) {
			t.Errorf("%s: __fern_arr_push does not read the count as a 32-bit word:\n%s", target, head)
		}
		if m := wide[target].FindString(string(asm)); m != "" {
			t.Errorf("%s: a push compares the count as a 64-bit load:\n%s", target, m)
		}
	}
}
