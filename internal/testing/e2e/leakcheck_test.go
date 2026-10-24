package e2e

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func runLeakCheckX86_64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, src, []string{"FERN_LEAKCHECK=1"})
	return runSplit(t, runX86_64Bin(runner, bin))
}

func runLeakCheckArm64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	return runLeakCheckArm64Args(t, src)
}

func runLeakCheckArm64Args(t *testing.T, src string, args ...string) (string, string, int) {
	t.Helper()
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, src, []string{"FERN_LEAKCHECK=1"})
	return runSplit(t, runArm64Bin(qemu, bin, args...))
}

func runSplit(t *testing.T, cmd *exec.Cmd) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

var leakCheckLineRe = regexp.MustCompile(`^leakcheck: allocs=(-?\d+) frees=(-?\d+) live_bytes=(-?\d+)( from=exit)?\n$`)

func parseLeakCheckLine(t *testing.T, stderr string) (allocs, frees, live int64) {
	t.Helper()
	allocs, frees, live, exited := parseLeakCheckLineFrom(t, stderr)
	if exited {
		t.Fatalf("the report says exit() printed it, but main returned: %q", stderr)
	}
	return allocs, frees, live
}

func parseLeakCheckLineExit(t *testing.T, stderr string) (allocs, frees, live int64) {
	t.Helper()
	allocs, frees, live, exited := parseLeakCheckLineFrom(t, stderr)
	if !exited {
		t.Fatalf("the report does not end ` from=exit`, but exit() printed it: %q", stderr)
	}
	return allocs, frees, live
}

func parseLeakCheckLineFrom(t *testing.T, stderr string) (allocs, frees, live int64, exited bool) {
	t.Helper()
	m := leakCheckLineRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("stderr is not a single leakcheck report line: %q", stderr)
	}
	allocs, _ = strconv.ParseInt(m[1], 10, 64)
	frees, _ = strconv.ParseInt(m[2], 10, 64)
	live, _ = strconv.ParseInt(m[3], 10, 64)
	return allocs, frees, live, m[4] != ""
}

const leakCheckBalancedSrc = `function main(): i32 {
    let i: i32 = 0;
    while (i < 100) {
        let a: usize = __alloc(64);
        __free(a, 64);
        i = i + 1;
    }
    return 0;
}`

const leakCheckRcDropSrc = `function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 50) {
        let row: i32[] = [i, i + 1, i + 2];
        sum = sum + row[0];
        i = i + 1;
    }
    if (sum == 1225) { return 0; }
    return 1;
}`

const leakCheckLeakySrc = `function main(): i32 {
    let a: usize = __alloc(60);
    let b: usize = __alloc(60);
    let c: usize = __alloc(60);
    __free(a, 60);
    if (b == c) { return 9; }
    return 42;
}`

const leakCheckExitBuiltinSrc = `function main(): i32 {
    let a: usize = __alloc(100);
    print("hello");
    exit(7);
    return 0;
}`

func TestX86_64LeakCheckBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckBalancedSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 100/100/0", allocs, frees, live)
	}
}

func TestX86_64LeakCheckRcDropBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckRcDropSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Errorf("expected a non-zero alloc count (one row per iteration)")
	}
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0 (precise drop frees every row)", allocs, frees, live)
	}
}

func TestX86_64LeakCheckLeakReported(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckLeakySrc)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (report must not clobber main's exit code)", code)
	}
	if stdout != "" {
		t.Errorf("stdout=%q, want empty (report goes to stderr only)", stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 3 || frees != 1 || live != 128 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 3/1/128", allocs, frees, live)
	}
}

func TestX86_64LeakCheckExitBuiltinReports(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckExitBuiltinSrc)
	if code != 7 {
		t.Errorf("exit=%d, want 7 (__fern_exit must preserve the code around the report)", code)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout=%q, want %q", stdout, "hello\n")
	}
	allocs, frees, live := parseLeakCheckLineExit(t, stderr)
	if allocs != 1 || frees != 0 || live != 104 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 1/0/104", allocs, frees, live)
	}
}

func TestArm64LeakCheckBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckBalancedSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 100/100/0", allocs, frees, live)
	}
}

func TestArm64LeakCheckLeakReported(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckLeakySrc)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (report must not clobber main's exit code)", code)
	}
	if stdout != "" {
		t.Errorf("stdout=%q, want empty (report goes to stderr only)", stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 3 || frees != 1 || live != 128 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 3/1/128", allocs, frees, live)
	}
}

func TestArm64LeakCheckExitBuiltinReports(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckExitBuiltinSrc)
	if code != 7 {
		t.Errorf("exit=%d, want 7 (__fern_exit must preserve the code around the report)", code)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout=%q, want %q", stdout, "hello\n")
	}
	allocs, frees, live := parseLeakCheckLineExit(t, stderr)
	if allocs != 1 || frees != 0 || live != 104 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 1/0/104", allocs, frees, live)
	}
}

const retainedParamSrc = `import "std/i32";
struct Tk { name: string, line: i32 }
function mkT(name: string, line: i32): Tk { return Tk { name: name, line: line }; }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let s: string = "id" + r.to_string();
        let t: Tk = mkT(s, r);
        acc = acc + t.name.len();
        r = r + 1;
    }
    return acc % 5;
}`

const retainedParamInlineSrc = `import "std/i32";
struct Tk { name: string, line: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let s: string = "id" + r.to_string();
        let t: Tk = Tk { name: s, line: r };
        acc = acc + t.name.len();
        r = r + 1;
    }
    return acc % 5;
}`

func TestLeakCheckRetainedParamX86_64(t *testing.T) {
	_, viaHelperErr, viaHelperCode := runLeakCheckX86_64(t, retainedParamSrc)
	_, inlineErr, inlineCode := runLeakCheckX86_64(t, retainedParamInlineSrc)
	if viaHelperCode != inlineCode {
		t.Fatalf("exit codes differ: helper %d, inline %d", viaHelperCode, inlineCode)
	}
	ha, hf, _ := parseLeakCheckLine(t, viaHelperErr)
	ia, iff, _ := parseLeakCheckLine(t, inlineErr)
	if ha != ia {
		t.Fatalf("allocs differ: helper %d, inline %d — fixture drift", ha, ia)
	}
	if hf < iff {
		t.Errorf("retaining callee frees %d of %d, inline form frees %d: the call leaks the caller's reference", hf, ha, iff)
	}
}

func TestLeakCheckRetainedParamArm64(t *testing.T) {
	_, viaHelperErr, viaHelperCode := runLeakCheckArm64(t, retainedParamSrc)
	_, inlineErr, inlineCode := runLeakCheckArm64(t, retainedParamInlineSrc)
	if viaHelperCode != inlineCode {
		t.Fatalf("exit codes differ: helper %d, inline %d", viaHelperCode, inlineCode)
	}
	ha, hf, _ := parseLeakCheckLine(t, viaHelperErr)
	ia, iff, _ := parseLeakCheckLine(t, inlineErr)
	if ha != ia {
		t.Fatalf("allocs differ: helper %d, inline %d — fixture drift", ha, ia)
	}
	if hf < iff {
		t.Errorf("retaining callee frees %d of %d, inline form frees %d: the call leaks the caller's reference", hf, ha, iff)
	}
}

const resultThreadReclaimSrc = `struct Lx { src: string, i: i32 }
struct TId { text: string }
struct TEof {}
type Tok = TId | TEof;
struct Res { lex: Lx, tok: Tok }
function scan(l: Lx): Res {
    let t: Tok = TId { text: slice_unchecked(l.src, l.i, l.i + 1) + "" };
    return Res { lex: Lx { src: l.src, i: l.i + 1 }, tok: t };
}
function run(src: string): i32 {
    let l: Lx = Lx { src: src, i: 0 };
    let out: Tok[] = [];
    while (l.i < 5) {
        let r = scan(l);
        l = r.lex;
        out = out.append(r.tok);
    }
    return out.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let n: i32 = 0;
    while (n < 100) {
        acc = acc + run("abcdefgh");
        n = n + 1;
    }
    return acc % 7;
}`

func TestLeakCheckResultThreadReclaimX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, resultThreadReclaimSrc)
	if code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want fully reclaimed (allocs==frees, live==0): the l = r.lex result-struct threading strands its cluster", allocs, frees, live)
	}
}

func TestLeakCheckResultThreadReclaimArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, resultThreadReclaimSrc)
	if code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want fully reclaimed (allocs==frees, live==0): the l = r.lex result-struct threading strands its cluster", allocs, frees, live)
	}
}

const scalarThreadReclaimSrc = `struct Lx { src: string, i: i32, line: i32 }
struct TId { text: string, line: i32 }
struct TPunct { text: string, line: i32 }
struct TEof { line: i32 }
type Tok = TId | TPunct | TEof;
struct Res { lex: Lx, tok: Tok }
function scan(l: Lx, start_line: i32): Res {
    let t: Tok = TId { text: slice_unchecked(l.src, l.i, l.i + 1) + "", line: start_line };
    return Res { lex: Lx { src: l.src, i: l.i + 1, line: l.line + 1 }, tok: t };
}
function run(src: string): i32 {
    let l: Lx = Lx { src: src, i: 0, line: 1 };
    let out: Tok[] = [];
    while (l.i < 8) {
        let start_line: i32 = l.line;
        if (l.src[l.i] == 46) {
            out = out.append(TPunct { text: ".", line: start_line });
            l = Lx { src: l.src, i: l.i + 1, line: l.line };
            continue;
        }
        let r = scan(l, start_line);
        l = r.lex;
        out = out.append(r.tok);
    }
    return out.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let n: i32 = 0;
    while (n < 100) {
        acc = acc + run("abc.def.g");
        n = n + 1;
    }
    return acc % 7;
}`

func TestLeakCheckScalarThreadReclaimX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, scalarThreadReclaimSrc)
	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want fully reclaimed: the scalar-arg exemption must credit the projection-threaded cursor param", allocs, frees, live)
	}
}

func TestLeakCheckScalarThreadReclaimArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, scalarThreadReclaimSrc)
	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want fully reclaimed: the scalar-arg exemption must credit the projection-threaded cursor param", allocs, frees, live)
	}
}

const loopConstructionMoveSrc = `function main(): i32 {
    let s: i32 = 0;
    let k: i32 = 0;
    while (k < 20) {
        let xs: i32[] = [1, 2, 3];
        let t = (xs, 99);
        s = s + t.0[2];
        k = k + 1;
    }
    return s % 7;
}`

const loopConstructionFreshSrc = `function main(): i32 {
    let s: i32 = 0;
    let k: i32 = 0;
    while (k < 20) {
        let t = ([1, 2, 3], 99);
        s = s + t.0[2];
        k = k + 1;
    }
    return s % 7;
}`

const loopAliasNoIncSrc = `function main(): i32 {
    let s: i32 = 0;
    let k: i32 = 0;
    let a0: i32[] = [1, 2, 3].append(4);
    while (k < 20) {
        let a1: i32[] = a0;
        s = s + a1[2];
        k = k + 1;
    }
    return s % 7;
}`

func TestX86_64LeakCheckLoopConstructionMove(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"bare-ident-element", loopConstructionMoveSrc, 4},
		{"fresh-literal-element", loopConstructionFreshSrc, 4},
		{"alias-without-inc", loopAliasNoIncSrc, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

func TestArm64LeakCheckLoopConstructionMove(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"bare-ident-element", loopConstructionMoveSrc, 4},
		{"fresh-literal-element", loopConstructionFreshSrc, 4},
		{"alias-without-inc", loopAliasNoIncSrc, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

const pureReadLenSrc = `import "std/i32";
struct Box { s: string, n: i32 }
function mk(s: string): Box { return Box { s: s, n: s.len() }; }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let key: string = "k" + r.to_string();
        let b: Box = mk(key);
        acc = acc + b.s.len();
        r = r + 1;
    }
    return acc % 7;
}`

const pureReadNoLenSrc = `import "std/i32";
struct Box { s: string, n: i32 }
function mk(s: string): Box { return Box { s: s, n: 0 }; }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let key: string = "k" + r.to_string();
        let b: Box = mk(key);
        acc = acc + b.s.len();
        r = r + 1;
    }
    return acc % 7;
}`

func TestLeakCheckPureReadLenX86_64(t *testing.T) {
	_, lenErr, lenCode := runLeakCheckX86_64(t, pureReadLenSrc)
	_, noLenErr, noLenCode := runLeakCheckX86_64(t, pureReadNoLenSrc)
	if lenCode != noLenCode {
		t.Fatalf("exit codes differ: len %d, nolen %d", lenCode, noLenCode)
	}
	la, lf, _ := parseLeakCheckLine(t, lenErr)
	na, nf, _ := parseLeakCheckLine(t, noLenErr)
	if la != na {
		t.Fatalf("allocs differ: len %d, nolen %d — fixture drift", la, na)
	}
	if lf < nf {
		t.Errorf("with s.len() frees %d of %d, without it frees %d: the pure-read receiver must not disqualify the counted-retain param", lf, la, nf)
	}
}

func TestLeakCheckPureReadLenArm64(t *testing.T) {
	_, lenErr, lenCode := runLeakCheckArm64(t, pureReadLenSrc)
	_, noLenErr, noLenCode := runLeakCheckArm64(t, pureReadNoLenSrc)
	if lenCode != noLenCode {
		t.Fatalf("exit codes differ: len %d, nolen %d", lenCode, noLenCode)
	}
	la, lf, _ := parseLeakCheckLine(t, lenErr)
	na, nf, _ := parseLeakCheckLine(t, noLenErr)
	if la != na {
		t.Fatalf("allocs differ: len %d, nolen %d — fixture drift", la, na)
	}
	if lf < nf {
		t.Errorf("with s.len() frees %d of %d, without it frees %d: the pure-read receiver must not disqualify the counted-retain param", lf, la, nf)
	}
}

const tupleElemArrayScalarSrc = `function main(): i32 {
    let total: i32 = 0;
    let k: i32 = 0;
    while (k < 50) {
        let t = (k, k + 1);
        let c = [t, (7, 8)];
        total = total + c[0].0 + c[1].1;
        k = k + 1;
    }
    return total % 251;
}`

const tupleElemArrayStringSrc = `function main(): i32 {
    let total: i32 = 0;
    let k: i32 = 0;
    while (k < 50) {
        let s: string = "ab" + "cd";
        let t = (s, k);
        let c = [t];
        total = total + c[0].0.len();
        k = k + 1;
    }
    return total % 251;
}`

func TestX86_64LeakCheckTupleElemArray(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"scalar-tuple-element", tupleElemArrayScalarSrc, 119},
		{"string-tuple-element", tupleElemArrayStringSrc, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

func TestArm64LeakCheckTupleElemArray(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"scalar-tuple-element", tupleElemArrayScalarSrc, 119},
		{"string-tuple-element", tupleElemArrayStringSrc, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

const tupleInTupleScalarSrc = `function main(): i32 {
    let total: i32 = 0;
    let k: i32 = 0;
    while (k < 50) {
        let t = (k, k + 1);
        let o = (t, 99);
        total = total + o.0.1 + o.1;
        k = k + 1;
    }
    return total % 251;
}`

const tupleInStructScalarSrc = `struct Holder { pair: (i32, i32), n: i32 }
function main(): i32 {
    let total: i32 = 0;
    let k: i32 = 0;
    while (k < 50) {
        let t = (k, k + 1);
        let h = Holder { pair: t, n: 3 };
        total = total + h.pair.1 + h.n;
        k = k + 1;
    }
    return total % 251;
}`

func TestX86_64LeakCheckNestedTupleElem(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"tuple-in-tuple", tupleInTupleScalarSrc},
		{"tuple-in-struct", tupleInStructScalarSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift (exit %d)", code)
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

func TestArm64LeakCheckNestedTupleElem(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"tuple-in-tuple", tupleInTupleScalarSrc},
		{"tuple-in-struct", tupleInStructScalarSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift (exit %d)", code)
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

const ctorRetainedSourceReadSrc = `function main(): i32 {
    let s: i32 = 0;
    let k: i32 = 0;
    while (k < 40) {
        let xs: i32[] = [4, 5, 6];
        let o = (xs, 9);
        s = s + xs[1] + o.1;
        k = k + 1;
    }
    return s % 251;
}`

const ctorRetainedContainerReadSrc = `function main(): i32 {
    let s: i32 = 0;
    let k: i32 = 0;
    while (k < 40) {
        let xs: i32[] = [4, 5, 6];
        let o = (xs, 9);
        s = s + o.0[1] + o.1;
        k = k + 1;
    }
    return s % 251;
}`

const ctorRetainedEnumPayloadSrc = `@noinline function keep(o: Option[i32[]]): i32 { return 0; }
function main(): i32 {
    let s: i32 = 0;
    let k: i32 = 0;
    while (k < 40) {
        let xs: i32[] = [4, 5, 6];
        let o = Some(xs);
        s = s + xs[1] + keep(o);
        k = k + 1;
    }
    return s % 251;
}`

const ctorOuterAliasSrc = `function main(): i32 {
    let s: i32 = 0;
    let k: i32 = 0;
    let a0: i32[] = [7, 8, 9];
    while (k < 40) {
        let a1: i32[] = a0;
        let o = (a1, 1);
        s = s + a1[2] + o.1;
        k = k + 1;
    }
    return s % 251;
}`

const ctorRetainedDropOrderSrc = `function main(): i32 {
    let xs: i32[] = [4, 5, 6].append(7);
    let o = (xs, 9);
    let s: i32 = xs[1] + o.1;
    return s % 251;
}`

func TestX86_64LeakCheckCtorRetainedDropOrder(t *testing.T) {
	e2eharness.BoxedProbes(t)
	_, stderr, code := runLeakCheckX86_64(t, ctorRetainedDropOrderSrc)
	if code != 14 {
		t.Fatalf("exit=%d, want 14 — the read is wrong, not just its accounting", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 2 || frees != 2 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 2/2/0 — the container's drop "+
			"ran before its source's, so nothing freed the buffer", allocs, frees, live)
	}
}

func TestArm64LeakCheckCtorRetainedDropOrder(t *testing.T) {
	e2eharness.BoxedProbes(t)
	_, stderr, code := runLeakCheckArm64(t, ctorRetainedDropOrderSrc)
	if code != 14 {
		t.Fatalf("exit=%d, want 14 — the read is wrong, not just its accounting", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 2 || frees != 2 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 2/2/0", allocs, frees, live)
	}
}

func TestX86_64LeakCheckCtorRetainedLoopSource(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"source-read-after", ctorRetainedSourceReadSrc, 58},
		{"container-read", ctorRetainedContainerReadSrc, 58},
		{"enum-payload", ctorRetainedEnumPayloadSrc, 200},
		{"outer-alias", ctorOuterAliasSrc, 149},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

func TestArm64LeakCheckCtorRetainedLoopSource(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"source-read-after", ctorRetainedSourceReadSrc, 58},
		{"container-read", ctorRetainedContainerReadSrc, 58},
		{"enum-payload", ctorRetainedEnumPayloadSrc, 200},
		{"outer-alias", ctorOuterAliasSrc, 149},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

const enumStringPayloadBoxSrc = `@noinline function keep(o: Option[string]): i32 { return 0; }
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 100) {
        let st: string = "ab" + "cd";
        let o = Some(st);
        t = t + st.len() + keep(o);
        k = k + 1;
    }
    return t % 251;
}`

const enumArrayPayloadBoxSrc = `@noinline function keep(o: Option[i32[]]): i32 { return 0; }
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 100) {
        let xs: i32[] = [1, 2];
        let o = Some(xs);
        t = t + xs[1] + keep(o);
        k = k + 1;
    }
    return t % 251;
}`

const enumScalarPayloadNoBoxSrc = `@noinline function keep(o: Option[i32]): i32 { return 0; }
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 100) {
        let o = Some(k);
        t = t + 1 + keep(o);
        k = k + 1;
    }
    return t % 251;
}`

const enumScalarFromCalleeSrc = `function mk(n: i32): Option[i32] { return Some(n); }
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 100) {
        let o = mk(k);
        t = t + 1;
        k = k + 1;
    }
    return t % 251;
}`

const enumScalarNestedStructSrc = `struct H { o: Option[i32], n: i32 }
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 100) {
        let h = H { o: Some(k), n: 1 };
        t = t + h.n;
        k = k + 1;
    }
    return t % 251;
}`

const enumScalarNestedTupleSrc = `function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 100) {
        let p = (Some(k), 1);
        t = t + p.1;
        k = k + 1;
    }
    return t % 251;
}`

func TestX86_64LeakCheckEnumStringPayloadBox(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"string-payload-boxed", enumStringPayloadBoxSrc, 149},
		{"array-payload-boxed", enumArrayPayloadBoxSrc, 200},
		{"scalar-payload-boxed", enumScalarPayloadNoBoxSrc, 100},
		{"scalar-from-pairform-callee", enumScalarFromCalleeSrc, 100},
		{"scalar-nested-in-struct", enumScalarNestedStructSrc, 100},
		{"scalar-nested-in-tuple", enumScalarNestedTupleSrc, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

func TestArm64LeakCheckEnumStringPayloadBox(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"string-payload-boxed", enumStringPayloadBoxSrc, 149},
		{"array-payload-boxed", enumArrayPayloadBoxSrc, 200},
		{"scalar-payload-boxed", enumScalarPayloadNoBoxSrc, 100},
		{"scalar-from-pairform-callee", enumScalarFromCalleeSrc, 100},
		{"scalar-nested-in-struct", enumScalarNestedStructSrc, 100},
		{"scalar-nested-in-tuple", enumScalarNestedTupleSrc, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

const toStringReclaimSrc = `import "std/i32";
import "std/i64";
import "std/u64";
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 50) {
        let s: string = k.to_string();
        let u: string = ((k as i64) * 1000000007).to_string();
        let v: string = ((k as u64) * (1234567891 as u64)).to_string();
        let w: string = (k * 1234567).to_binary();
        t = t + s.len() + u.len() + v.len() + w.len();
        k = k + 1;
    }
    if (t == 2378) { return 0; }
    return 1;
}`

const radixReclaimSrc = `import "std/i32";
import "core/int";
function main(): i32 {
    let acc: i32 = 0;
    let k: i32 = 1;
    while (k < 60) {
        let h: string = int.int_to_string_radix(k * 7919, 16);
        let b: string = int.int_to_string_radix(0 - (k * 7919), 2);
        let i: i32 = 0;
        while (i < h.len()) { acc = (acc * 31 + (h[i] as i32)) % 100003; i = i + 1; }
        i = 0;
        while (i < b.len()) { acc = (acc * 31 + (b[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

const rgbHexReclaimSrc = `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let k: i32 = 1;
    while (k < 40) {
        let s: string = (k * 66049).to_rgb_hex();
        let i: i32 = 0;
        while (i < s.len()) { acc = (acc * 31 + (s[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

var toStringReclaimCases = []struct {
	name        string
	src         string
	exit        int
	fullReclaim bool
}{
	{"to_string-i32-i64-u64-binary", toStringReclaimSrc, 0, true},
	{"int_to_string_radix", radixReclaimSrc, 143, true},
	{"to_rgb_hex", rgbHexReclaimSrc, 29, false},
}

func checkToStringReclaim(t *testing.T, stderr string, code, wantExit int, fullReclaim bool) {
	t.Helper()
	if code != wantExit {
		t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, wantExit)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if fullReclaim && (frees != allocs || live != 0) {
		t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
	}
}

func TestX86_64LeakCheckToStringReclaim(t *testing.T) {
	for _, tc := range toStringReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			checkToStringReclaim(t, stderr, code, tc.exit, tc.fullReclaim)
		})
	}
}

func TestArm64LeakCheckToStringReclaim(t *testing.T) {
	for _, tc := range toStringReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			checkToStringReclaim(t, stderr, code, tc.exit, tc.fullReclaim)
		})
	}
}

const argTempReclaimTempRecvSrc = `import "std/i32";
import "std/string";
function main(): i32 {
    let acc: i32 = 0;
    let k: i32 = 1;
    while (k < 40) {
        let s: string = (k * 66049).to_binary().pad_start(40, '0');
        let i: i32 = 0;
        while (i < s.len()) { acc = (acc * 31 + (s[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

const argTempReclaimBoundRecvSrc = `import "std/i32";
import "std/string";
function main(): i32 {
    let acc: i32 = 0;
    let k: i32 = 1;
    while (k < 40) {
        let h: string = (k * 66049).to_binary();
        let s: string = h.pad_start(40, '0');
        let i: i32 = 0;
        while (i < s.len()) { acc = (acc * 31 + (s[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

const argTempReclaimPassThroughSrc = `import "std/i32";
import "std/string";
function main(): i32 {
    let acc: i32 = 0;
    let k: i32 = 1;
    while (k < 40) {
        let s: string = (k * 66049).to_binary().pad_start(2, '0');
        let i: i32 = 0;
        while (i < s.len()) { acc = (acc * 31 + (s[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

var argTempReclaimCases = []struct {
	name string
	src  string
	exit int
}{
	{"temp-receiver", argTempReclaimTempRecvSrc, 240},
	{"bound-receiver", argTempReclaimBoundRecvSrc, 240},
	{"pass-through-arm", argTempReclaimPassThroughSrc, 24},
}

func checkArgTempReclaim(t *testing.T, stderr string, code, wantExit int) {
	t.Helper()
	if code != wantExit {
		t.Fatalf("exit=%d, want %d — the string is wrong, which for the pass-through leg "+
			"means the post-call dec freed a buffer the result still referenced", code, wantExit)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift (are the intermediates still > 7 bytes?)")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
	}
}

func TestX86_64LeakCheckStringArgTempReclaim(t *testing.T) {
	for _, tc := range argTempReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			checkArgTempReclaim(t, stderr, code, tc.exit)
		})
	}
}

func TestArm64LeakCheckStringArgTempReclaim(t *testing.T) {
	for _, tc := range argTempReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			checkArgTempReclaim(t, stderr, code, tc.exit)
		})
	}
}

func TestX86_64OpenPathCopyFreed(t *testing.T) {
	const n = 64
	prog := func(path string) string {
		return `function main(): i32 {
    let i: i32 = 0;
    let errs: i32 = 0;
    while (i < ` + fmt.Sprint(n) + `) {
        match (open_reader("` + path + `")) {
            Ok(r) => { r.close(); },
            Err(e) => { errs = errs + 1; }
        }
        i = i + 1;
    }
    if (errs != ` + fmt.Sprint(n) + `) { return 1; }
    return 0;
}`
	}
	short := "/no/pth"
	long := "/nonexistent/" + strings.Repeat("d", 187)
	if len(short) != 7 || len(long) != 200 {
		t.Fatalf("path lengths %d / %d", len(short), len(long))
	}
	_, errShort, exitShort := runLeakCheckX86_64(t, prog(short))
	_, errLong, exitLong := runLeakCheckX86_64(t, prog(long))
	if exitShort != 0 || exitLong != 0 {
		t.Fatalf("exit %d / %d, want 0 (every open must fail with ENOENT)", exitShort, exitLong)
	}
	_, _, liveShort := parseLeakCheckLine(t, errShort)
	_, _, liveLong := parseLeakCheckLine(t, errLong)
	if liveShort != liveLong {
		t.Errorf("live_bytes short=%d long=%d: the open helper's NUL-terminated path copy is not returned after openat (%d bytes over %d opens)", liveShort, liveLong, liveLong-liveShort, n)
	}
}
