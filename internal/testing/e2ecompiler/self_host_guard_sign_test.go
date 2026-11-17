package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Each scan starts at an index that can be negative. The guard of the loop
// proves the index below the length and does not prove its sign.
const guardSignFuncs = `@noinline
function spaces(s: string, from: i32): i32 {
    let i = from;
    while (i < s.len() && s[i] == b' ') { i += 1; }
    return i;
}

@noinline
function zeros(a: i32[], from: i32): i32 {
    let i = from;
    while (i < a.len() && a[i] == 0) { i += 1; }
    return i;
}

@noinline
function wide(a: i64[], from: i32): i32 {
    let i = from;
    while (i < a.len() && a[i] == 0i64) { i += 1; }
    return i;
}

@noinline
function words(v: [i32], from: i32): i32 {
    let i = from;
    while (i < v.len() && v[i] == 0) { i += 1; }
    return i;
}

@noinline
function bytes(v: [u8], from: i32): i32 {
    let i = from;
    while (i < v.len() && v[i] == 0u8) { i += 1; }
    return i;
}

@noinline
function long(n: i64): i32 {
    return (n - 4294967296i64) as i32;
}
`

var guardSignCases = []struct {
	name string
	body string
	exit int
	text string
}{
	{"in_range", `let xs: i32[] = [0, 0, 1, 0]; let bs: u8[] = [0u8, 0u8, 9u8];
    return spaces("   x", 0) + zeros(xs, 0) + wide([0i64, 0i64, 5i64], 1) + words(xs[1:4], 0) + bytes(bs[0:3], 1) + spaces("  ", 9) + 23;`, 42, ""},
	{"string", `return spaces("   x", 0 - 1);`, 134, "string index out of range"},
	{"string_lowest", `return spaces("   x", 0 - 2147483647 - 1);`, 134, "string index out of range"},
	{"array", `return zeros([0, 0, 1], 0 - 1);`, 134, "array index out of range"},
	{"array_lowest", `return zeros([0, 0, 1], 0 - 2147483647 - 1);`, 134, "array index out of range"},
	{"array_from_i64", `return zeros([0, 0, 1], long(4294967293i64));`, 134, "array index out of range"},
	{"wide_array", `return wide([0i64, 0i64, 5i64], 0 - 3);`, 134, "array index out of range"},
	{"word_view", `let xs: i32[] = [0, 0, 1, 0]; return words(xs[1:4], 0 - 1);`, 134, "array index out of range"},
	{"byte_view", `let bs: u8[] = [0u8, 0u8, 9u8]; return bytes(bs[0:3], 0 - 2);`, 134, "array index out of range"},
}

func guardSignBody(t *testing.T, asm []byte, name, end string) string {
	t.Helper()
	loc := regexp.MustCompile(`(?m)^__fn_` + name + `\.r:\n`).FindIndex(asm)
	if loc == nil {
		t.Fatalf("no function %s in the listing", name)
	}
	body := string(asm[loc[1]:])
	if stop := strings.Index(body, end); stop >= 0 {
		body = body[:stop]
	}
	return body
}

// A read that a guard proves below the length tests only the sign of its
// index. It does not compare the index with the length again, and its
// function keeps no frame.
func TestSelfHostGuardLeavesASignTest(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "sign.fern")
	prog := guardSignFuncs + "\nfunction main(): i32 {\n    " + guardSignCases[0].body + "\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	x86 := filepath.Join(dir, "sign.s")
	if combined, err := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", x86, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting x86-64: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(x86)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []struct{ name, trap string }{{"spaces", "__fern_str_oob_abort"}, {"zeros", "__fern_oob_abort"}, {"wide", "__fern_oob_abort"}} {
		body := guardSignBody(t, asm, fn.name, ".cfi_endproc")
		if !regexp.MustCompile(`testl (%\w+), (%\w+)\n\s+js ` + fn.trap + `\.l0\n`).MatchString(body) {
			t.Errorf("%s: no sign test of the index before the read:\n%s", fn.name, body)
		}
		if strings.Contains(body, "jae ") {
			t.Errorf("%s: the read compares the index with the length again:\n%s", fn.name, body)
		}
		if strings.Contains(body, "pushq") {
			t.Errorf("%s: the function has a frame:\n%s", fn.name, body)
		}
	}
	arm := filepath.Join(dir, "sign-arm64.s")
	if combined, err := exec.Command(h.cli, "-target", "arm64-linux", "-emit", "asm", "-o", arm, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting arm64: %v\n%s", err, combined)
	}
	armAsm, err := os.ReadFile(arm)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []struct{ name, trap string }{{"spaces", "__fern_str_oob_abort_leaf"}, {"zeros", "__fern_oob_abort_leaf"}, {"wide", "__fern_oob_abort_leaf"}} {
		body := guardSignBody(t, armAsm, fn.name, ".ltorg")
		if !regexp.MustCompile(`cmp w\d+, #0\n\s+b\.ge 1f\n\s+b ` + fn.trap + `\n`).MatchString(body) {
			t.Errorf("arm64 %s: no sign test of the index before the read:\n%s", fn.name, body)
		}
		if strings.Contains(body, "b.lo ") {
			t.Errorf("arm64 %s: the read compares the index with the length again:\n%s", fn.name, body)
		}
		if strings.Contains(body, "stp x29") {
			t.Errorf("arm64 %s: the function has a frame:\n%s", fn.name, body)
		}
	}
}

// A negative index passes the guard, so the read must abort, with the same
// message and status as the full check.
func TestSelfHostGuardSignTestAborts(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, c := range guardSignCases {
			t.Run(fmt.Sprintf("%s/%s", target, c.name), func(t *testing.T) {
				stderr, code := cli.exitOf(t, guardSignFuncs+"\nfunction main(): i32 {\n    "+c.body+"\n}\n", target)
				if code != c.exit {
					t.Fatalf("exit %d, want %d\n%s", code, c.exit, stderr)
				}
				if !strings.Contains(stderr, c.text) {
					t.Errorf("stderr %q does not hold %q", stderr, c.text)
				}
			})
		}
	}
}

// byte_at has 16 operations that the inliner of small leaves counts, which is
// its limit, when its read keeps the full check.
const guardSignLeafProg = `function byte_at(s: string, i: i32): u8 {
    if (i < s.len()) { return s[i]; }
    return 0u8;
}

@noinline
function scan(s: string, from: i32): i32 {
    let i = from;
    let n = 0;
    while (byte_at(s, i) != b'"') {
        if (byte_at(s, i) == b'\\') { n += 1; }
        if (byte_at(s, i) == 0u8) { return n; }
        i += 1;
    }
    return n + i;
}

function main(): i32 { return scan("ab\\c\"", 0) + 37; }
`

// The sign test, its drop and the data pointer of the string are one read for
// the inliner of small leaves, so a leaf at the limit is still spliced.
func TestSelfHostGuardSignTestKeepsTheLeaf(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "leaf.fern")
	if err := os.WriteFile(src, []byte(guardSignLeafProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range []struct{ target, call, sign string }{
		{"x86-64-linux", "call __fn_byte_at", `testl %\w+, %\w+\n\s+js __fern_str_oob_abort`},
		{"arm64-linux", "bl __fn_byte_at", `cmp w\d+, #0\n\s+b\.ge 1f\n\s+b __fern_str_oob_abort`},
	} {
		out := filepath.Join(dir, tg.target+".s")
		if combined, err := exec.Command(h.cli, "-target", tg.target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("emitting %s: %v\n%s", tg.target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(asm), tg.call) {
			t.Errorf("%s: scan calls byte_at", tg.target)
		}
		end := ".cfi_endproc"
		if tg.target == "arm64-linux" {
			end = ".ltorg"
		}
		body := guardSignBody(t, asm, "scan", end)
		if n := len(regexp.MustCompile(tg.sign).FindAllString(body, -1)); n != 3 {
			t.Errorf("%s: scan has %d sign tests, want 3:\n%s", tg.target, n, body)
		}
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, guardSignLeafProg, target); code != 42 {
			t.Errorf("%s: exit %d, want 42\n%s", target, code, stderr)
		}
	}
}
