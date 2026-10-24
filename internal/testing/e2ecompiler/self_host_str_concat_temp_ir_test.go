package e2ecompiler

import (
	"os/exec"
	"testing"
)

var strConcatTempIRCases = []struct {
	name         string
	src          string
	expected     int
	wantReclaims int
	scope        string
}{
	{"two-literals",
		`function main(): i32 { let r: string = "hi" + "!"; return r.len(); }`,
		3, -1, ""},
	{"chain-intermediate",
		`function main(): i32 { let a: string = "x"; let b: string = "yy"; let c: string = "zzz"; let r: string = a + b + c; return r.len(); }`,
		6, -1, ""},
	{"chain-churn-safe",
		`function main(): i32 { let pre: string = "aa"; let suf: string = "bb"; let t: i32 = 0; let k: i32 = 0; while (k < 5000000) { let r: string = pre + "x" + suf; t = (t + r.len()) % 7; k = k + 1; } return 0; }`,
		0, -1, ""},
	{"ident-operands-result-only",
		`@noinline function f(a: string, b: string): i32 { return (a + b).len() + a.len() + b.len(); } function main(): i32 { return f("ab", "cde"); }`,
		10, 1, "f"},
	{"tostring-operand-churn",
		`import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let r: string = "n" + w.to_string(); acc = (acc + r.len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let r2: string = "n" + i.to_string(); acc = (acc + r2.len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`,
		0, -1, ""},
	{"tostring-operand-len",
		`import "std/i32";
function main(): i32 { let w: i32 = 47; return ("n" + w.to_string()).len(); }`,
		3, -1, ""},
	{"tostring-cast-operand-len",
		`import "std/i32";
function main(): i32 { let k: i32 = 12; return ("v" + (k as u32).to_string()).len(); }`,
		3, -1, ""},
	{"arith-tostring-operand-churn",
		`import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let r: string = "n" + (w % 8).to_string(); acc = (acc + r.len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let r2: string = "n" + (i % 8).to_string(); acc = (acc + r2.len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`,
		0, -1, ""},
	{"neg-tostring-operand-churn",
		`import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 1;
    while (w < 200) { let r: string = "v" + (-w).to_string(); acc = (acc + r.len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 1;
    while (i < 5000) { let r2: string = "v" + (-i).to_string(); acc = (acc + r2.len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`,
		0, -1, ""},
	{"arith-tostring-string-operand-alias-safe",
		`import "std/i32";
function main(): i32 {
    let s: string = "abcd";
    if (("x" + ("a" + s).to_string()).len() != 6) { return 97; }
    if (s.len() != 4) { return 96; }
    if (__rc_underflow_count() != 0) { return 95; }
    return 0;
}`,
		0, -1, ""},
	{"tostring-string-recv-alias-safe",
		`import "std/i32";
function main(): i32 {
    let s: string = "abcd";
    if (("x" + s.to_string()).len() != 5) { return 97; }
    if (s.len() != 4) { return 96; }
    return 0;
}`,
		0, 1, ""},
}

func TestSelfHostStrConcatTempIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range strConcatTempIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			reclaims := countUserStrFreeReclaims(asm)
			if tc.scope != "" {
				reclaims = countCallsInFn(t, asm, tc.scope, "__fn___fern_str_free")
			}
			if tc.wantReclaims < 0 && reclaims == 0 {
				t.Errorf("%s: expected a fresh-operand reclaim (call __fn___fern_str_free), found none — the temp leaks", tc.name)
			}
			if tc.wantReclaims >= 0 && reclaims != tc.wantReclaims {
				t.Errorf("%s: expected exactly %d reclaim site(s), found %d — extra sites mean an aliased operand was mis-freed (over-release / UAF risk)", tc.name, tc.wantReclaims, reclaims)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
