package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// A borrowed array handed to a call the frame outlives is bracketed — retained
// before, released after — only where the callee may grow it in place
// (ssaunits.grows_buffer). `scan` hands its table to two byte scans and to a
// function that only reads it, once per loop turn, and carries neither half of
// the bracket. `widen` hands its array to one that appends to it, and keeps the
// bracket: the exit code is what says the append copied rather than growing
// the caller's buffer under it.
//
// Through the CLI, since the bracket is the semantic lowering's (ssarc).
const lentArrayBracketProg = `@noinline function reads(o: u8[]): i32 { return o.len(); }
@noinline function grows(o: u8[]): i32 { var x: u8[] = o.append(1 as u8); return x.len(); }
@noinline function scan(s: string, set: u8[]): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { n = n + __scan_set(s, i, set) + __count_runs(s, 0, set) + reads(set); i = i + 1; }
    return n + set.len();
}
@noinline function widen(set: u8[]): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { n = n + grows(set); i = i + 1; }
    return n + set.len();
}
function main(): i32 {
    var t: u8[] = __alloc_u8(256);
    t = t.with(97, 1 as u8);
    return (scan("xxa", t) + widen(t)) % 200;
}
`

func TestSelfHostLentArrayBracket(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "lent.fern")
	if err := os.WriteFile(src, []byte(lentArrayBracketProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bracket := []*regexp.Regexp{regexp.MustCompile(`__fern_arr_dec`), regexp.MustCompile(`__fern_rc_inc|_rcinc[0-9]+:`)}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		scan := lentArrayBody(t, string(asm), "scan")
		widen := lentArrayBody(t, string(asm), "widen")
		for _, re := range bracket {
			if re.MatchString(scan) {
				t.Errorf("%s: scan brackets a table no callee grows (%s):\n%s", target, re, scan)
			}
			if !re.MatchString(widen) {
				t.Errorf("%s: widen lost the bracket around a callee that appends (%s):\n%s", target, re, widen)
			}
		}
	}
	want := (3*(2+1+256) + 256 + 3*257 + 256) % 200
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != want {
			t.Errorf("%s: exit %d, want %d", tg.target, got, want)
		}
	}
}

// lentArrayBody is the CLI's listing of `name` from its label to its first
// return.
func lentArrayBody(t *testing.T, asm, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\n__fn_` + name + `\.r:\n(.*?)\n\s+ret\n`).FindStringSubmatch(asm)
	if m == nil {
		t.Fatalf("__fn_%s.r not found in the listing", name)
	}
	return m[1]
}
