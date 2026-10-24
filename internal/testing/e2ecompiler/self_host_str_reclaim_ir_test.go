package e2ecompiler

import (
	"os/exec"
	"testing"
)

var strReclaimIRCases = []struct {
	name        string
	src         string
	expected    int
	mustReclaim bool
	scope       string
}{
	{"loop-concat-nonescaping",
		`function main(): i32 { let tag: string = "row"; let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s: string = tag + "!"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		16, true, ""},
	{"loop-to-upper-nonescaping",
		`import "std/string";
function main(): i32 { let base: string = "abc"; let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s: string = base.to_ascii_upper(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		12, true, ""},
	{"loop-call-result-concat",
		`import "std/i32";
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s: string = ((65 + (i % 3)) as u8).to_ascii_string() + "x"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		8, true, ""},
	{"scope-exit-concat",
		`@noinline function ids(s: string): string { return s; } function main(): i32 { let s: string = ids("hi") + "!"; return s.len(); }`,
		3, true, ""},
	{"str-churn-safe",
		`function main(): i32 { let tag: string = "abcd"; let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let s: string = tag + "ef"; sum = (sum + s.len()) % 100; i = i + 1; } return sum; }`,
		0, true, ""},
	{"aliased-reclaimed-once",
		`@noinline function mk(a: string, b: string): i32 { let s: string = a + b; let t: string = s; return s.len() + t.len(); } function main(): i32 { return mk("ab", "c"); }`,
		6, true, "mk"},
	{"returned-not-reclaimed",
		`@noinline function h(x: string, y: string): string { let s: string = x + y; return s; } function main(): i32 { return h("xy", "z").len(); }`,
		3, false, "h"},
	{"loop-i32-to-string",
		`import "std/i32";
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 12) { let s: string = i.to_string(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		14, true, ""},
	{"loop-unannotated-i32-to-string",
		`import "std/i32";
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 12) { let s = i.to_string(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		14, true, ""},
	{"loop-unannotated-concat",
		`function main(): i32 { let tag: string = "row"; let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s = tag + "!"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		16, true, ""},
	{"loop-unannotated-to-upper",
		`import "std/string";
function main(): i32 { let base: string = "abc"; let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s = base.to_ascii_upper(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		12, true, ""},
	{"unannotated-int-add-not-reclaimed",
		`function main(): i32 { let a: i32 = 3; let b: i32 = 4; let n = a + b; return n; }`,
		7, false, ""},
	{"i32-to-string-churn-safe",
		`import "std/i32";
function main(): i32 { let ok: i32 = 0; let i: i32 = 0; while (i < 5000000) { let s: string = i.to_string(); if (s.len() < 1) { ok = 1; } i = i + 1; } return ok; }`,
		0, true, ""},
}

func TestSelfHostStrReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range strReclaimIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			reclaims := countUserStrFreeReclaims(asm)
			if tc.scope != "" {
				reclaims = countCallsInFn(t, asm, tc.scope, "__fn___fern_str_free")
			}
			if tc.mustReclaim && reclaims == 0 {
				t.Errorf("%s: expected a fresh-string reclaim (call __fn___fern_str_free), found none — the string leaks", tc.name)
			}
			if !tc.mustReclaim && reclaims != 0 {
				t.Errorf("%s: expected NO fresh-string reclaim (escaping/aliased), found %d — a double-free / UAF risk", tc.name, reclaims)
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
