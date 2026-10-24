package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPegModule(t *testing.T) {
	src := `import "std/peg";
function main(): i32 {
    let rules: Map[string, peg.Pattern] = map_new(4);
    rules = rules.insert("b", PChoice([
        PSeq([PLit("("), PRef("b"), PLit(")"), PRef("b")]),
        PLit("")
    ]));
    rules = rules.insert("all", PSeq([PRef("b"), PCap("tail", PPlus(peg.peg_digit())), PEof]));
    let Ok(g) = peg.peg_grammar(rules, "all") else { return 90; };
    let Ok(r) = peg.peg_match(g, "(()(()))42") else { return 91; };
    if (r.caps.get_or("tail", "") != "42") { return 92; }
    if (ok(peg.peg_match(g, "((x"))) { return 93; }
    let kw: peg.Pattern = PSeq([PLit("if"), PNot(peg.peg_alnum())]);
    if (ok(peg.peg_match_pattern(kw, "iffy"))) { return 94; }
    if (!ok(peg.peg_match_pattern(kw, "if (x)"))) { return 95; }
    return 55;
}
function ok(r: Result[peg.PegMatch, peg.PegError]): boolean {
    if let Ok(_) = r { return true; }
    return false;
}`
	const want = 55
	t.Run("interp", func(t *testing.T) {
		if code := runInterpByte(t, src); code != want {
			t.Errorf("interp exit = %d, want %d", code, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, src); code != want {
			t.Errorf("arm64 exit = %d, want %d", code, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, src); code != want {
			t.Errorf("x86_64 exit = %d, want %d", code, want)
		}
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, src); code != want {
			t.Errorf("wasm exit = %d, want %d", code, want)
		}
	})
}

func TestPegLeftRecursion(t *testing.T) {
	src := `import "std/peg";
function main(): i32 {
    let r1: Map[string, peg.Pattern] = map_new(2);
    r1 = r1.insert("l", PSeq([PRef("l"), PLit("a")]));
    let Ok(g1) = peg.peg_grammar(r1, "l") else { return 90; };
    if (ok(peg.peg_match(g1, "aaa"))) { return 91; }
    let r2: Map[string, peg.Pattern] = map_new(2);
    r2 = r2.insert("a", PSeq([PRef("b"), PLit("x")]));
    r2 = r2.insert("b", PRef("a"));
    let Ok(g2) = peg.peg_grammar(r2, "a") else { return 92; };
    if (ok(peg.peg_match(g2, "xxx"))) { return 93; }
    let r3: Map[string, peg.Pattern] = map_new(2);
    r3 = r3.insert("r", PChoice([PSeq([PLit("a"), PRef("r")]), PLit("")]));
    let deep: string = "";
    let i: i32 = 0;
    while (i < 200) { deep = deep + "a"; i = i + 1; }
    let Ok(g3) = peg.peg_grammar(r3, "r") else { return 94; };
    if (!ok(peg.peg_match(g3, deep))) { return 95; }
    let r4: Map[string, peg.Pattern] = map_new(2);
    r4 = r4.insert("b", PChoice([PSeq([PLit("("), PRef("b"), PLit(")")]), PLit("")]));
    let Ok(g4) = peg.peg_grammar(r4, "b") else { return 96; };
    if (!ok(peg.peg_match(g4, "((()))"))) { return 97; }
    return 55;
}
function ok(r: Result[peg.PegMatch, peg.PegError]): boolean {
    if let Ok(_) = r { return true; }
    return false;
}`
	const want = 55
	t.Run("interp", func(t *testing.T) {
		if code := runInterpByte(t, src); code != want {
			t.Errorf("interp exit = %d, want %d", code, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, src); code != want {
			t.Errorf("arm64 exit = %d, want %d", code, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, src); code != want {
			t.Errorf("x86_64 exit = %d, want %d", code, want)
		}
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, src); code != want {
			t.Errorf("wasm exit = %d, want %d", code, want)
		}
	})
}

func TestX86_64PegTapRunsNatively(t *testing.T) {
	_, runner := x86_64Tooling(t)
	fern := buildFernCLI(t)
	out := filepath.Join(t.TempDir(), "peg_tap")
	if o, err := exec.Command(fern, "-target", "x86-64-linux", "-o", out,
		"../../../tests/stdlib/peg_test.fern").CombinedOutput(); err != nil {
		t.Fatalf("native compile of peg_test.fern failed: %v\n%s", err, o)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(out)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], out)...)
	}
	tap, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("TAP binary exit = %d, want 0\n%s", code, tap)
	}
	for _, w := range []string{"# tests 18", "# pass 18", "# fail 0"} {
		if s := string(tap); !strings.Contains(s, w) {
			t.Errorf("TAP output missing %q:\n%s", w, s)
		}
	}
}
