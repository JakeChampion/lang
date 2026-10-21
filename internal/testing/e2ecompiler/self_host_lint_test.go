package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// lintRun writes files into a fresh directory, runs the self-host CLI's
// `-lint` over the paths in args (relative to it, flags passed through) and
// returns stdout, stderr and the exit status.
func lintRun(t *testing.T, files map[string]string, args ...string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), append([]string{"-lint", "-color", "never"}, args...)...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

var lintScoreRe = regexp.MustCompile("function `([^`]+)` has a cyclomatic complexity of (\\d+)")

// lintScores is every reported function's score under max=1, which reports
// every function scoring 2 or more; an unreported function scored 1.
func lintScores(t *testing.T, src string) map[string]int {
	t.Helper()
	out, errOut, code := lintRun(t, map[string]string{"t.fern": src}, "-lint-opt", "cyclomatic-complexity.max=1", "t.fern")
	if code != 0 {
		t.Fatalf("-lint exit %d\n%s%s", code, out, errOut)
	}
	scores := map[string]int{}
	for _, m := range lintScoreRe.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[2])
		scores[m[1]] = n
	}
	return scores
}

func scoreOrOne(scores map[string]int, name string) int {
	if n, ok := scores[name]; ok {
		return n
	}
	return 1
}

// The complexity model: one for the entry path, one per fork, and what does
// not fork.
func TestSelfHostLintScoreModel(t *testing.T) {
	cases := []struct {
		name string
		want int
		body string
	}{
		{"straight-line", 1, `let a: i32 = 1; return a;`},
		{"if", 2, `if (n > 0) { return 1; } return 0;`},
		{"if-else", 2, `if (n > 0) { return 1; } else { return 2; }`},
		{"else-if chain", 3, `if (n > 0) { return 1; } else if (n < 0) { return 2; } else { return 3; }`},
		{"if-expression", 2, `return if (n > 0) { 1 } else { 2 };`},
		{"while", 2, `while (n > 0) { n = n - 1; } return n;`},
		{"c-for", 2, `for (let i: i32 = 0; i < n; i = i + 1) { n = n; } return n;`},
		{"loop", 2, `loop { break; } return n;`},
		{"and", 3, `if (n > 0 && n < 9) { return 1; } return 0;`},
		{"or", 3, `if (n > 0 || n < 9) { return 1; } return 0;`},
		{"and-or chain", 4, `if (n > 0 && n < 9 || n == 42) { return 1; } return 0;`},
		{"non-logical binary", 1, `return n + 1 * 2 - 3;`},
		{"break and continue", 2, `while (n > 0) { n = n - 1; continue; } return n;`},
		{"assert", 1, `assert(n > 0); return n;`},
		{"todo", 1, `todo;`},
		{"boolean match", 3, `match (n > 0) { true => { return 1; }, false => { return 0; } }`},
		{"literal match", 3, `match (n) { 0 => { return 1; }, 1 => { return 2; }, _ => { return 0; } }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function f(n: i32): i32 {\n" + tc.body + "\n}\n"
			if got := scoreOrOne(lintScores(t, src), "f"); got != tc.want {
				t.Errorf("score = %d, want %d for:\n%s", got, tc.want, src)
			}
		})
	}
}

// Match arms: each that can fail to match, plus each guard; a nested pattern
// counts as the arm written, not the matches it lowers to.
func TestSelfHostLintScoreMatchArms(t *testing.T) {
	const enum = "enum E { A(i32), B(i32) }\n"
	cases := []struct {
		name string
		want int
		body string
	}{
		{"two variants", 3, "match (e) { A(x) => { return x; }, B(x) => { return x + 1; } }"},
		{"wildcard is the fall-through", 2, "match (e) { A(x) => { return x; }, _ => { return 0; } }"},
		{"guard adds a condition", 3, "match (e) { A(x) when x > 0 => { return x; }, _ => { return 0; } }"},
		{"if let", 2, "if let A(x) = e { return x; }\nreturn 0;"},
		{"nested pattern", 4, "let o: Option[E] = Some(e);\nmatch (o) { Some(A(x)) => { return x; }, Some(_) => { return 1; }, None => { return 0; } }"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := enum + "function f(e: E): i32 {\n" + tc.body + "\n}\n"
			if got := scoreOrOne(lintScores(t, src), "f"); got != tc.want {
				t.Errorf("score = %d, want %d for:\n%s", got, tc.want, src)
			}
		})
	}
}

// for-in, `?`, a lambda's body folding into its function, and a method named
// by its receiver.
func TestSelfHostLintScoreForEachTryLambdaReceiver(t *testing.T) {
	scores := lintScores(t, `
struct Counter { n: i32 }
function (c: Counter) bump(): i32 {
  if (c.n > 0) { return c.n + 1; }
  return 1;
}
function each(xs: i32[]): i32 {
  let t: i32 = 0;
  for x in xs { t = t + x; }
  return t;
}
function tried(o: Option[i32]): Option[i32] {
  let v: i32 = o?;
  return Some(v + 1);
}
function lifted(xs: i32[]): i32[] {
  return apply(xs, (x: i32): i32 => { if (x > 0) { return x; } return 0 - x; });
}
function apply(xs: i32[], f: (i32) => i32): i32[] {
  return xs;
}
`)
	for name, want := range map[string]int{"Counter.bump": 2, "each": 2, "tried": 2, "lifted": 2} {
		if got := scoreOrOne(scores, name); got != want {
			t.Errorf("%s: score = %d, want %d (scores %v)", name, got, want, scores)
		}
	}
}

const lintNoisyFn = `function noisy(n: i32): i32 {
    if (n > 0) { return 1; }
    if (n < 0) { return 2; }
    return 0;
}
`

func lintFindings(t *testing.T, src string) string {
	t.Helper()
	out, errOut, code := lintRun(t, map[string]string{"t.fern": src}, "-lint-opt", "cyclomatic-complexity.max=2", "t.fern")
	if code != 0 {
		t.Fatalf("-lint exit %d\n%s%s", code, out, errOut)
	}
	return out
}

// `// fern-lint: allow` covers the next construct, past doc comments and
// blank lines, or its own line when code precedes it; allow-file the file.
func TestSelfHostLintSuppressionForms(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"no directive", lintNoisyFn, 1},
		{"directive on the line above", "// fern-lint: allow cyclomatic-complexity\n" + lintNoisyFn, 0},
		{"directive above a doc comment", "// fern-lint: allow cyclomatic-complexity\n// noisy dispatches a flat table.\n\n" + lintNoisyFn, 0},
		{"directive naming several rules", "// fern-lint: allow cyclomatic-complexity, cyclomatic-complexity\n" + lintNoisyFn, 0},
		{"allow-file covers a function further down", "// fern-lint: allow-file cyclomatic-complexity\nfunction quiet(): i32 { return 0; }\n" + lintNoisyFn, 0},
		{"allow covers only the next construct", "// fern-lint: allow cyclomatic-complexity\n" + lintNoisyFn + "\n" + strings.Replace(lintNoisyFn, "noisy", "noisier", 1), 1},
		{"trailing directive covers its own line", strings.Replace(lintNoisyFn, "{\n", "{ // fern-lint: allow cyclomatic-complexity\n", 1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := lintFindings(t, tc.src)
			if got := strings.Count(out, "[cyclomatic-complexity]"); got != tc.want {
				t.Errorf("got %d complexity findings, want %d:\n%s", got, tc.want, out)
			}
		})
	}
}

// A malformed directive is a lint-directive finding, and silences nothing.
func TestSelfHostLintBadDirectives(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"unknown rule", "// fern-lint: allow cyclomatic-complexety\n" + lintNoisyFn, "names no such lint rule"},
		{"unknown verb", "// fern-lint: deny cyclomatic-complexity\n" + lintNoisyFn, "unknown lint directive"},
		{"no rule named", "// fern-lint: allow\n" + lintNoisyFn, "names no rule"},
		{"dangling", lintNoisyFn + "\n// fern-lint: allow cyclomatic-complexity\n", "covers no code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := lintFindings(t, tc.src)
			if !strings.Contains(out, "warning[lint-directive]: ") || !strings.Contains(out, tc.want) {
				t.Errorf("want a lint-directive finding containing %q:\n%s", tc.want, out)
			}
			if !strings.Contains(out, "[cyclomatic-complexity]") {
				t.Errorf("the complexity finding should still be reported:\n%s", out)
			}
		})
	}
}

// The rendered finding, its advice, the separation between findings, the
// summary, and the exit status a deny severity gives.
func TestSelfHostLintRenderingAndExit(t *testing.T) {
	src := lintNoisyFn + strings.Replace(lintNoisyFn, "noisy", "noisier", 1)
	out, _, code := lintRun(t, map[string]string{"t.fern": src}, "-lint-opt", "cyclomatic-complexity.max=2", "t.fern")
	want := "t.fern:1:1: warning[cyclomatic-complexity]: function `noisy` has a cyclomatic complexity of 3, over the limit of 2\n" +
		"    function noisy(n: i32): i32 {\n" +
		"    ^\n" +
		"    help: split out the branch-heavy part, or raise the limit for this site with `// fern-lint: allow cyclomatic-complexity`\n" +
		"\n" +
		"t.fern:6:1: warning[cyclomatic-complexity]: function `noisier` has a cyclomatic complexity of 3, over the limit of 2\n" +
		"    function noisier(n: i32): i32 {\n" +
		"    ^\n" +
		"    help: split out the branch-heavy part, or raise the limit for this site with `// fern-lint: allow cyclomatic-complexity`\n" +
		"2 warnings generated\n"
	if code != 0 || out != want {
		t.Errorf("exit %d\n--- got ---\n%s--- want ---\n%s", code, out, want)
	}
	out, _, code = lintRun(t, map[string]string{"t.fern": lintNoisyFn}, "-lint-opt", "cyclomatic-complexity.max=2", "-lint-set", "cyclomatic-complexity=deny", "t.fern")
	if code != 1 || !strings.Contains(out, "error[cyclomatic-complexity]") || !strings.HasSuffix(out, "1 error generated\n") {
		t.Errorf("deny: exit %d\n%s", code, out)
	}
	out, _, code = lintRun(t, map[string]string{"t.fern": lintNoisyFn}, "-lint-set", "cyclomatic-complexity=allow", "-lint-opt", "cyclomatic-complexity.max=2", "t.fern")
	if code != 0 || out != "" {
		t.Errorf("allow: exit %d, output %q", code, out)
	}
}

// The governing fern.toml's [lint] tables configure the run, -lint-set wins
// over them, and a bad setting is a usage error naming where it was spelled.
func TestSelfHostLintConfiguration(t *testing.T) {
	files := map[string]string{
		"fern.toml":  "[package]\nname = \"p\"\n\n[lint]\ncyclomatic-complexity = \"deny\"\n\n[lint.options]\ncyclomatic-complexity.max = 2\n",
		"src/a.fern": lintNoisyFn,
	}
	out, _, code := lintRun(t, files, "src")
	if code != 1 || !strings.Contains(out, "error[cyclomatic-complexity]") {
		t.Errorf("manifest deny: exit %d\n%s", code, out)
	}
	out, _, code = lintRun(t, files, "-lint-set", "cyclomatic-complexity=warn", "src")
	if code != 0 || !strings.Contains(out, "warning[cyclomatic-complexity]") {
		t.Errorf("-lint-set over the manifest: exit %d\n%s", code, out)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-lint-set", "cyclomatic-complexity=loud", "src"}, `unknown severity "loud"`},
		{[]string{"-lint-set", "no-such-rule=warn", "src"}, `unknown lint rule "no-such-rule"`},
		{[]string{"-lint-opt", "cyclomatic-complexity.min=3", "src"}, `unknown option "min"`},
		{[]string{"-lint-opt", "cyclomatic-complexity.max=0", "src"}, "must be at least 1"},
		{[]string{"-lint-set", "nonsense", "src"}, "must be spelled RULE=SEVERITY"},
		{[]string{"fern.toml"}, "not a .fern source"},
	} {
		_, errOut, code := lintRun(t, map[string]string{"fern.toml": "[package]\nname = \"p\"\n", "src/a.fern": lintNoisyFn}, tc.args...)
		if code != 2 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
	}
	unquoted := map[string]string{"fern.toml": "[lint]\ncyclomatic-complexity = deny\n", "a.fern": lintNoisyFn}
	if _, errOut, code := lintRun(t, unquoted, "a.fern"); code != 2 || !strings.Contains(errOut, `fern.toml: line 2: [lint] cyclomatic-complexity: expected a quoted string or a number, got "deny"`) {
		t.Errorf("unquoted severity: exit %d, stderr %q", code, errOut)
	}
	bad := map[string]string{"fern.toml": "[lint.options]\nmax = 3\n", "a.fern": lintNoisyFn}
	if _, errOut, code := lintRun(t, bad, "a.fern"); code != 2 || !strings.Contains(errOut, "fern.toml: line 2: [lint.options] key \"max\" must be spelled <rule>.<option>") {
		t.Errorf("bad [lint.options] key: exit %d, stderr %q", code, errOut)
	}
}

// A file the linter cannot read as a module fails the run like a denied rule,
// whether it does not parse or, as a literate document, does not tangle: the
// error is printed and the exit status is 1, not the 0 of a clean lint.
func TestSelfHostLintUnreadableModule(t *testing.T) {
	for _, tc := range []struct {
		name, file, src, want string
	}{
		{"parse error", "a.fern", "function f(: i32 {\n  return 1;\n}\n", "error[P001]"},
		{"no root chunk", "doc.fern.md", "# doc\n", `no root chunk "<<*>>"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, code := lintRun(t, map[string]string{tc.file: tc.src, "ok.fern": "function f(): i32 {\n  return 1;\n}\n"}, tc.file, "ok.fern")
			if code != 1 || !strings.Contains(errOut, tc.want) {
				t.Errorf("exit %d, stderr %q; want 1 and %q", code, errOut, tc.want)
			}
		})
	}
}

// `-lint-rules` lists every rule with its default severity and options.
func TestSelfHostLintRules(t *testing.T) {
	out, err := exec.Command(e2eharness.SelfHostCLI(t), "-lint-rules").Output()
	want := "cyclomatic-complexity  [warn]\n" +
		"    function has more independent control-flow paths than the configured limit\n" +
		"    option cyclomatic-complexity.max = 10 (default)\n"
	if err != nil || string(out) != want {
		t.Errorf("-lint-rules: %v\n--- got ---\n%s--- want ---\n%s", err, out, want)
	}
}

// Splitting a function over the limit into smaller ones over it raises the
// number of findings and lowers the summed distance over the limit — the
// reason the ratchet sums distance instead of counting functions.
func TestSelfHostLintSplittingImprovesExcess(t *testing.T) {
	fn := func(name string, forks int) string {
		var b strings.Builder
		b.WriteString("function " + name + "(n: i32): i32 {\n")
		for i := 0; i < forks; i++ {
			b.WriteString("  if (n == " + strconv.Itoa(i) + ") { return " + strconv.Itoa(i) + "; }\n")
		}
		return b.String() + "  return 0;\n}\n"
	}
	measure := func(src string) (count, excess int) {
		out, errOut, code := lintRun(t, map[string]string{"t.fern": src}, "t.fern")
		if code != 0 {
			t.Fatalf("-lint exit %d\n%s%s", code, out, errOut)
		}
		for _, m := range lintScoreRe.FindAllStringSubmatch(out, -1) {
			n, _ := strconv.Atoi(m[2])
			count++
			excess += n - 10
		}
		return count, excess
	}
	wc, we := measure(fn("whole", 39))
	sc, se := measure(fn("a", 13) + fn("b", 13) + fn("c", 13))
	if wc != 1 || we != 30 || sc != 3 || se != 12 {
		t.Errorf("whole: %d finding(s), excess %d; split: %d, excess %d — want 1 and 30, then 3 and 12", wc, we, sc, se)
	}
}
