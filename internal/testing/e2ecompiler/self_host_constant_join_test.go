package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// kept hands its argument back whole after a look inside, so each caller's
// match meets the two words of the result at a join: a phi per word and a
// boolean phi the match branches on. direct reads each word in one arm, so
// the join splits. merged reads the Ok word after the arms meet again, so
// its join stays.
const constantJoinProgram = `enum Fail { Short(string), Long(string, i32) }

@noinline function probe(n: i32): Result[i32, Fail] {
    if (n > 5) {
        return Err(Fail.Short("over"));
    }
    return Ok(n * 2);
}

@noinline function kept(r: Result[i32, Fail]): Result[i32, Fail] {
    if let Ok(v) = r {
        if (v == 4) {
            return Err(Fail.Long("four", v));
        }
    }
    return r;
}

@noinline function direct(n: i32): i32 {
    match (kept(probe(n))) {
        Ok(v) => {
            return v + 1;
        },
        Err(e) => {
            match (e) {
                Fail.Short(s) => {
                    return 0 - s.len();
                },
                Fail.Long(s, k) => {
                    return 0 - 100 - k - s.len();
                }
            }
        }
    }
    return 0;
}

@noinline function merged(n: i32): i32 {
    let r: Result[i32, Fail] = kept(probe(n));
    let bonus: i32 = 0;
    if let Ok(v) = r {
        bonus = v;
    }
    match (r) {
        Ok(v) => {
            return v + bonus;
        },
        Err(e) => {
            return bonus - 1;
        }
    }
    return 0;
}

function main(): i32 {
    if (direct(1) != 3 || direct(3) != 7 || direct(2) != 0 - 108 || direct(9) != 0 - 4) {
        return 1;
    }
    if (merged(1) != 4 || merged(2) != 0 - 1 || merged(9) != 0 - 1) {
        return 2;
    }
    if (__rc_underflow_count() != 0) {
        return 3;
    }
    return 42;
}
`

func semanticDumpOf(t *testing.T, h ssaBackendHost, src, name string) string {
	t.Helper()
	cmd := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out.s"), src, h.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SEM_DUMP="+name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dumping %s: %v\n%s", name, err, out)
	}
	marker := "FERN_SEM_DUMP: " + name + " "
	at := strings.Index(string(out), marker)
	if at < 0 {
		t.Fatalf("no semantic dump of %s:\n%s", name, out)
	}
	body := string(out)[at:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end+3]
	}
	return body
}

func TestSelfHostConstantJoinSplit(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "constant_join.fern")
	if err := os.WriteFile(src, []byte(constantJoinProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	phi := regexp.MustCompile(`= phi `)
	direct := semanticDumpOf(t, h, src, "direct")
	if !strings.Contains(direct, "__pair_word") {
		t.Fatalf("direct does not take kept's result in two words, so the shape below is not this pass's:\n%s", direct)
	}
	if phi.MatchString(direct) {
		t.Errorf("direct still meets kept's result at a join:\n%s", direct)
	}
	merged := semanticDumpOf(t, h, src, "merged")
	if !phi.MatchString(merged) || !strings.Contains(merged, "const_bool") {
		t.Errorf("merged reads the Ok word after its arms meet, so its join must stay:\n%s", merged)
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
			t.Errorf("%s: exit %d, want 42 (1: direct, 2: merged, 3: a count went below zero)", tg.target, got)
		}
	}
}

func TestSelfHostConstantJoinSplitCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "constant_join.fern")
	if err := os.WriteFile(src, []byte(constantJoinProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
	if exit != 42 {
		t.Fatalf("leakcheck: exit = %d, want 42\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
	stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
	if exit != 42 || forArrStructSanitizerFault(stderr, true) {
		t.Fatalf("sanitize: exit = %d, want 42, and no sanitizer report\n%s", exit, stderr)
	}
}
