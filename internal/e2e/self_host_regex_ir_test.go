package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// regexEngineSrc is the std/regex matcher, inlined so each self-host case is a
// single small module (the self-host asm/wasm drivers compile one source from
// stdin; they don't resolve `import "std/regex"`). It is a copy of
// internal/stdlib/std/regex.fern's bodies — the stdlib module is what ships;
// this proves the same code lowers through the self-host IR path.
const regexEngineSrc = `
function __re_atom_len(p: string, ri: i32): i32 {
    if (p[ri] == 91) {
        var j: i32 = ri + 1;
        if (j < p.len() && p[j] == 94) { j = j + 1; }
        if (j < p.len() && p[j] == 93) { j = j + 1; }
        while (j < p.len() && p[j] != 93) { j = j + 1; }
        if (j < p.len()) { return (j - ri) + 1; }
        return j - ri;
    }
    return 1;
}
function __re_atom_matches(p: string, ri: i32, alen: i32, c: i32): boolean {
    var a0: i32 = p[ri];
    if (a0 == 46) { return true; }
    if (a0 == 91) {
        var j: i32 = ri + 1;
        var neg: boolean = false;
        if (j < ri + alen && p[j] == 94) { neg = true; j = j + 1; }
        var found: boolean = false;
        var end: i32 = ri + alen - 1;
        while (j < end) {
            if (p[j + 1] == 45 && j + 2 < end) {
                if (c >= p[j] && c <= p[j + 2]) { found = true; }
                j = j + 3;
            } else {
                if (c == p[j]) { found = true; }
                j = j + 1;
            }
        }
        if (neg) { return !found; }
        return found;
    }
    return c == a0;
}
function __re_here(p: string, ri: i32, t: string, ti: i32): boolean {
    if (ri >= p.len()) { return true; }
    if (p[ri] == 36 && ri + 1 >= p.len()) { return ti >= t.len(); }
    var alen: i32 = __re_atom_len(p, ri);
    var nxt: i32 = ri + alen;
    var q: i32 = 0;
    if (nxt < p.len()) {
        if (p[nxt] == 42 || p[nxt] == 43 || p[nxt] == 63) { q = p[nxt]; }
    }
    if (q == 42) { return __re_star(0, p, ri, alen, nxt + 1, t, ti); }
    if (q == 43) { return __re_star(1, p, ri, alen, nxt + 1, t, ti); }
    if (q == 63) {
        if (ti < t.len() && __re_atom_matches(p, ri, alen, t[ti])) {
            if (__re_here(p, nxt + 1, t, ti + 1)) { return true; }
        }
        return __re_here(p, nxt + 1, t, ti);
    }
    if (ti < t.len() && __re_atom_matches(p, ri, alen, t[ti])) {
        return __re_here(p, nxt, t, ti + 1);
    }
    return false;
}
function __re_star(min: i32, p: string, ri: i32, alen: i32, rest: i32, t: string, ti: i32): boolean {
    var k: i32 = ti;
    while (k < t.len() && __re_atom_matches(p, ri, alen, t[k])) { k = k + 1; }
    var count: i32 = k - ti;
    while (count >= min) {
        if (__re_here(p, rest, t, ti + count)) { return true; }
        count = count - 1;
    }
    return false;
}
function regex_match(pattern: string, text: string): boolean {
    if (pattern.len() > 0 && pattern[0] == 94) { return __re_here(pattern, 1, text, 0); }
    var ti: i32 = 0;
    while (true) {
        if (__re_here(pattern, 0, text, ti)) { return true; }
        if (ti >= text.len()) { return false; }
        ti = ti + 1;
    }
    return false;
}
`

// regexIRCases exercise the std/regex matcher through the self-host stack-IR
// path (#2680): literals + search, `.`/`^`/`$` anchors, the `*`/`+`/`?`
// quantifiers, and `[...]` classes with ranges and negation. Each program
// returns 1 when the pattern matches, 0 otherwise — covering both a matching
// and a deliberately non-matching case for the anchored class. Kept one
// regex per program (small) so each routes "ir" and stays well under the
// self-host wasm data-segment size where a many-literal program would not.
var regexIRCases = []struct {
	name     string
	main     string
	expected int
}{
	{"literal-search", `function main(): i32 { if (regex_match("abc", "xxabcyy")) { return 1; } return 0; }`, 1},
	{"anchored-dot", `function main(): i32 { if (regex_match("^a.c$", "axc")) { return 1; } return 0; }`, 1},
	{"star-backtrack", `function main(): i32 { if (regex_match("a*b", "aaab")) { return 1; } return 0; }`, 1},
	{"optional", `function main(): i32 { if (regex_match("colou?r", "color")) { return 1; } return 0; }`, 1},
	{"class-plus", `function main(): i32 { if (regex_match("[0-9]+", "abc123")) { return 1; } return 0; }`, 1},
	{"negated-class", `function main(): i32 { if (regex_match("[^0-9]", "123a")) { return 1; } return 0; }`, 1},
	{"range-anchored", `function main(): i32 { if (regex_match("^[A-Z][a-z]*$", "Hello")) { return 1; } return 0; }`, 1},
	{"anchored-no-match", `function main(): i32 { if (regex_match("^[0-9]+$", "12a45")) { return 1; } return 0; }`, 0},
}

// TestSelfHostRegexIRX86_64 routes each case through the self-hosted x86-64
// driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostRegexIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range regexIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(regexEngineSrc + "\n" + tc.main)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
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

// TestSelfHostRegexIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir).
func TestSelfHostRegexIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host regex wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range regexIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(regexEngineSrc + "\n" + tc.main)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "regex_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("regex wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
