package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fault and Sink.peek are no larger than a call, so each is spliced into all
// three of its callers. fault is a free function, so it then loses its body.
// Sink.peek reads a cell and returns an enum, so neither is a leaf.
const tinySpliceProgram = `enum Fault { Short(i32), Gone }
struct Sink { n: i32, err: Cell[Option[Fault]] }
function fault(s: Sink): Option[Fault] {
    return s.err.get();
}
function (s: Sink) peek(): Option[Fault] {
    return s.err.get();
}
@noinline function overflow(s: Sink): Result[Sink, Fault] {
    s.err.set(Some(Short(s.n)));
    return Err(Short(s.n));
}
@noinline function kept(s: Sink): Result[Sink, Fault] {
    if (s.n >= 100) {
        return overflow(s);
    }
    if let Some(e) = fault(s) {
        return Err(e);
    }
    if let Some(e) = s.peek() {
        return Err(e);
    }
    return Ok(s);
}
@noinline function code(s: Sink): i32 {
    let a: i32 = match (fault(s)) { Some(Short(n)) => { n }, Some(Gone()) => { 0 - 1 }, None => { 0 } };
    let b: i32 = match (s.peek()) { Some(Short(n)) => { n }, Some(Gone()) => { 0 - 1 }, None => { 0 } };
    return a + b;
}
@noinline function failed(s: Sink): boolean {
    let a: boolean = match (fault(s)) { Some(_) => { true }, None => { false } };
    let b: boolean = match (s.peek()) { Some(_) => { true }, None => { false } };
    return a && b;
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    let total: i32 = 0;
    let s: Sink = Sink { n: 0, err: cell_new(None) };
    let i: i32 = 0;
    while (i < 120) {
        s = Sink { ...s, n: i };
        match (kept(s)) {
            Ok(w) => { total = total + w.n; },
            Err(Short(n)) => { total = total + 1000 * n; },
            Err(Gone()) => { total = total + 1; }
        }
        i = i + 1;
    }
    print_int(total); print("");
    print_int(code(s)); print("");
    if (failed(s)) { print("failed"); }
    return 0;
}
`

func TestSelfHostTinySplice(t *testing.T) {
	callers := []string{"kept", "code", "failed"}
	runSemanticProgramSplicing(t, "tiny-splice", tinySpliceProgram, append([]string{"overflow"}, callers...),
		[]string{"fault"}, semInlineWants("2194950\n238\nfailed\n"), callers...)

	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "tiny_splice.fern")
	if err := os.WriteFile(src, []byte(tinySpliceProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		path := filepath.Join(filepath.Dir(src), target+".s")
		out, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", path, src, h.stdlib).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", target, err, out)
		}
		asm, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		call := "call __fn_"
		if target == "arm64-linux" {
			call = "bl __fn_"
		}
		for _, caller := range callers {
			body := condBranchBody(t, string(asm), caller)
			for _, callee := range []string{"fault", "Sink__peek"} {
				if strings.Contains(body, call+callee+"\n") || strings.Contains(body, call+callee+".r\n") {
					t.Errorf("%s: %s still calls %s", target, caller, callee)
				}
			}
		}
	}
}
