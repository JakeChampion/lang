package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// loweredArithmetic matches a negation or a binary minus in the graph that
// seminline is handed, before the splices and the late fold.
var loweredArithmetic = regexp.MustCompile(`= k(9 \[\d+,\d+,\]|10 \[\d+,\]) imm=0 -\n`)

// operandConstant matches the constants 1, 2 and 7 that minus and gap fold
// away: each one is only an operand of the folded operation.
var operandConstant = regexp.MustCompile(`= k1 \[\] imm=[127] \n`)

func loweredDumpOf(t *testing.T, h ssaBackendHost, src, name string) string {
	t.Helper()
	cmd := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out.s"), src, h.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SEM_DUMP="+name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dumping %s: %v\n%s", name, err, out)
	}
	at := strings.Index(string(out), "FUNC "+name+" ")
	if at < 0 {
		t.Fatalf("no lowered dump of %s:\n%s", name, out)
	}
	body := string(out)[at:]
	if end := strings.Index(body, "FERN_SEM_DUMP: "); end >= 0 {
		body = body[:end]
	}
	return body
}

// minus returns a negated literal on each path, and gap the difference of two
// literals. wrap negates the least i32, which does not fit when negated, so
// its negation stays.
const negationFoldProgram = `@noinline function minus(n: i32): i32 {
    if (n > 0) {
        return -1;
    }
    return -2;
}

@noinline function gap(): i32 {
    return 0 - 7;
}

@noinline function wrap(): i32 {
    let m = -2147483647 - 1;
    return -m;
}

function main(): i32 {
    if (minus(1) != -1 || minus(0) != -2) {
        return 1;
    }
    if (wrap() != -2147483647 - 1) {
        return 2;
    }
    if (gap() != -7) {
        return 3;
    }
    return 42;
}
`

func TestSelfHostNegationFold(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "negation_fold.fern")
	if err := os.WriteFile(src, []byte(negationFoldProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"minus", "gap"} {
		lowered := loweredDumpOf(t, h, src, name)
		if loweredArithmetic.MatchString(lowered) {
			t.Errorf("the lowering of %s computes a constant at run time:\n%s", name, lowered)
		}
		if operandConstant.MatchString(lowered) {
			t.Errorf("the lowering of %s keeps the constant of a folded operand:\n%s", name, lowered)
		}
	}
	if !loweredArithmetic.MatchString(loweredDumpOf(t, h, src, "wrap")) {
		t.Errorf("the lowering of wrap folds a negation that does not fit in i32")
	}
	minus := semanticDumpOf(t, h, src, "minus")
	if strings.Contains(minus, "unary -") {
		t.Errorf("minus negates a constant at run time:\n%s", minus)
	}
	if !strings.Contains(minus, "const_int -1") || !strings.Contains(minus, "const_int -2") {
		t.Errorf("minus does not return the constants -1 and -2:\n%s", minus)
	}
	if strings.Contains(minus, "= phi ") {
		t.Errorf("minus meets its two constant returns at a join:\n%s", minus)
	}
	wrap := semanticDumpOf(t, h, src, "wrap")
	if !strings.Contains(wrap, "unary -") {
		t.Errorf("wrap folds a negation that does not fit in i32:\n%s", wrap)
	}
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
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Errorf("%s: exit %d, want 42 (1: minus, 2: wrap, 3: gap)", tg.target, got)
		}
	}
}
