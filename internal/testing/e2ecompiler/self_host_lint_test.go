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
		"    option cyclomatic-complexity.max = 10 (default)\n" +
		"change-risk  [warn]\n" +
		"    function is complex and mostly untested: complexity^2 * (1 - coverage)^3 + complexity, scored from the -cover run -lint-cover names\n" +
		"    option change-risk.max = 30 (default)\n"
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

// lintForkFn is a function of `forks` consecutive `if`s on lines 2 through
// forks+1 and a return on the line after, so its complexity is forks+1 and
// a -cover run instruments forks+1 of its lines.
func lintForkFn(name string, forks int) string {
	var b strings.Builder
	b.WriteString("function " + name + "(n: i32): i32 {\n")
	for i := 0; i < forks; i++ {
		b.WriteString("  if (n == " + strconv.Itoa(i) + ") { return " + strconv.Itoa(i) + "; }\n")
	}
	return b.String() + "  return 0;\n}\n"
}

// coverRows is a -cover stream for `file`: one row per line in `hits`, keyed
// by line number.
func coverRows(file string, hits map[int]int) string {
	var b strings.Builder
	for line, n := range hits {
		b.WriteString("fern-cover: " + file + ":" + strconv.Itoa(line) + " " + strconv.Itoa(n) + "\n")
	}
	return b.String()
}

// The change-risk score: complexity^2 * (1 - coverage)^3 + complexity,
// rounded, over the rows between a function's first line and the last line
// its body reaches. A function the run holds no row for is not scored.
func TestSelfHostLintChangeRisk(t *testing.T) {
	// Four copies of the same 3-fork function at lines 1, 6, 11 and 16, with
	// 0, 1, 2 and 3 of their 3 instrumented lines hit, then an 11-fork one at
	// line 21 with nothing hit, then one with no rows at all.
	src := lintForkFn("none", 2) + lintForkFn("third", 2) + lintForkFn("most", 2) + lintForkFn("all", 2) +
		lintForkFn("big", 10) + lintForkFn("unmeasured", 2)
	hits := map[int]int{2: 0, 3: 0, 4: 0, 7: 1, 8: 0, 9: 0, 12: 1, 13: 1, 14: 0, 17: 1, 18: 1, 19: 1}
	for l := 22; l <= 32; l++ {
		hits[l] = 0
	}
	files := map[string]string{"t.fern": src, "cov.txt": coverRows("t.fern", hits)}
	out, errOut, code := lintRun(t, files, "-lint-cover", "cov.txt", "t.fern")
	want := "t.fern:21:1: warning[change-risk]: function `big` has a change risk of 132: complexity 11 with 0.0% of its 11 lines covered, over the limit of 30\n" +
		"    function big(n: i32): i32 {\n" +
		"    ^\n" +
		"    help: test it, or split out the branch-heavy part; `// fern-lint: allow change-risk` exempts this site\n" +
		"\n" +
		"t.fern:21:1: warning[cyclomatic-complexity]: function `big` has a cyclomatic complexity of 11, over the limit of 10\n" +
		"    function big(n: i32): i32 {\n" +
		"    ^\n" +
		"    help: split out the branch-heavy part, or raise the limit for this site with `// fern-lint: allow cyclomatic-complexity`\n" +
		"2 warnings generated\n"
	if code != 0 || out != want || errOut != "" {
		t.Errorf("default limit: exit %d, stderr %q\n--- got ---\n%s--- want ---\n%s", code, errOut, out, want)
	}
	// 3^2 * 1 + 3 = 12; 9 * (2/3)^3 = 2.67 -> 3, plus 3 = 6; 9 * (1/3)^3 =
	// 0.33 -> 0, plus 3 = 3; and 3 for the fully covered one.
	out, _, code = lintRun(t, files, "-lint-cover", "cov.txt", "-lint-set", "cyclomatic-complexity=allow", "-lint-opt", "change-risk.max=2", "t.fern")
	riskRe := regexp.MustCompile("function `([^`]+)` has a change risk of (\\d+): complexity 3 with ([0-9.]+%) of its 3 lines covered")
	got := map[string]string{}
	for _, m := range riskRe.FindAllStringSubmatch(out, -1) {
		got[m[1]] = m[2] + " at " + m[3]
	}
	wantRisks := map[string]string{"none": "12 at 0.0%", "third": "6 at 33.3%", "most": "3 at 66.7%", "all": "3 at 100.0%"}
	if code != 0 || len(got) != len(wantRisks) {
		t.Errorf("max=2: exit %d, %d change-risk findings:\n%s", code, len(got), out)
	}
	for name, want := range wantRisks {
		if got[name] != want {
			t.Errorf("%s: risk %q, want %q", name, got[name], want)
		}
	}
	if strings.Contains(out, "`unmeasured`") {
		t.Errorf("a function with no rows was scored:\n%s", out)
	}
	// deny fails the run; a suppression on the function or the file silences it.
	out, _, code = lintRun(t, files, "-lint-cover", "cov.txt", "-lint-set", "change-risk=deny", "t.fern")
	if code != 1 || !strings.Contains(out, "error[change-risk]: function `big`") {
		t.Errorf("deny: exit %d\n%s", code, out)
	}
	for _, directive := range []string{"// fern-lint: allow change-risk\n", "// fern-lint: allow-file change-risk\n"} {
		shifted := map[int]int{}
		for l, n := range hits {
			shifted[l+1] = n
		}
		quiet := map[string]string{"t.fern": directive + src, "cov.txt": coverRows("t.fern", shifted)}
		if directive == "// fern-lint: allow change-risk\n" {
			// The directive covers only `none`; `big` still fires.
			out, _, _ = lintRun(t, quiet, "-lint-cover", "cov.txt", "-lint-set", "cyclomatic-complexity=allow", "-lint-opt", "change-risk.max=2", "t.fern")
			if strings.Contains(out, "`none`") || !strings.Contains(out, "`big`") {
				t.Errorf("allow on the first function:\n%s", out)
			}
			continue
		}
		out, _, code = lintRun(t, quiet, "-lint-cover", "cov.txt", "-lint-set", "cyclomatic-complexity=allow", "t.fern")
		if code != 0 || out != "" {
			t.Errorf("allow-file: exit %d, output %q", code, out)
		}
	}
}

// A run names a file as the compile was told it and the lint as it was
// walked; one spelled with more or fewer leading directories still matches.
func TestSelfHostLintChangeRiskPaths(t *testing.T) {
	hits := map[int]int{2: 0, 3: 0, 4: 0}
	big := lintForkFn("noisy", 2)
	for _, tc := range []struct{ name, covered, linted string }{
		{"same spelling", "src/a.fern", "src/a.fern"},
		{"run is absolute", "/work/pkg/src/a.fern", "src/a.fern"},
		{"run is bare", "a.fern", "src"},
		{"lint path has a trailing slash", "src/a.fern", "src/"},
		{"lint path has two trailing slashes", "src/a.fern", "src//"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"src/a.fern": big, "cov.txt": coverRows(tc.covered, hits)}
			out, errOut, code := lintRun(t, files, "-lint-cover", "cov.txt", "-lint-opt", "change-risk.max=5", tc.linted)
			if code != 0 || !strings.Contains(out, "src/a.fern:1:1: warning[change-risk]: function `noisy` has a change risk of 12") {
				t.Errorf("exit %d, stderr %q\n%s", code, errOut, out)
			}
		})
	}
}

// The rule refuses to pass vacuously: configuring it without a run, a run
// that cannot be read or holds no rows, and a run naming none of the linted
// files are all usage errors.
func TestSelfHostLintChangeRiskRefusals(t *testing.T) {
	noisy := map[string]string{"t.fern": lintNoisyFn}
	for _, tc := range []struct {
		name  string
		files map[string]string
		args  []string
		want  string
	}{
		{"severity set, no run", noisy, []string{"-lint-set", "change-risk=deny", "t.fern"}, "change-risk is configured but no coverage was given: pass -lint-cover FILE"},
		{"option set, no run", noisy, []string{"-lint-opt", "change-risk.max=5", "t.fern"}, "change-risk is configured but no coverage was given"},
		{"manifest sets it, no run", map[string]string{"t.fern": lintNoisyFn, "fern.toml": "[package]\nname = \"p\"\n\n[lint]\nchange-risk = \"deny\"\n"}, []string{"t.fern"}, "change-risk is configured but no coverage was given"},
		{"unreadable run", noisy, []string{"-lint-cover", "missing.txt", "t.fern"}, "-lint-cover: cannot read missing.txt"},
		{"no rows", map[string]string{"t.fern": lintNoisyFn, "cov.txt": "program noise\n"}, []string{"-lint-cover", "cov.txt", "t.fern"}, "-lint-cover cov.txt: no fern-cover rows"},
		{"malformed row", map[string]string{"t.fern": lintNoisyFn, "cov.txt": "fern-cover: t.fern:x 1\n"}, []string{"-lint-cover", "cov.txt", "t.fern"}, "-lint-cover cov.txt: line 1:"},
		{"names other files", map[string]string{"t.fern": lintNoisyFn, "cov.txt": "fern-cover: other.fern:2 1\n"}, []string{"-lint-cover", "cov.txt", "t.fern"}, "-lint-cover cov.txt covers none of the linted files: it names other.fern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := lintRun(t, tc.files, tc.args...)
			if code != 2 || !strings.Contains(errOut, tc.want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want 2 and %q", code, out, errOut, tc.want)
			}
		})
	}
	// A rule set to allow scores nothing, so it needs no run, whatever else
	// names it, and a run naming none of the linted files is not refused.
	other := map[string]string{"t.fern": lintNoisyFn, "cov.txt": "fern-cover: other.fern:2 1\n"}
	for _, tc := range []struct {
		files map[string]string
		args  []string
	}{
		{noisy, []string{"-lint-set", "change-risk=allow", "t.fern"}},
		{noisy, []string{"-lint-set", "change-risk=allow", "-lint-opt", "change-risk.max=5", "t.fern"}},
		{other, []string{"-lint-set", "change-risk=allow", "-lint-cover", "cov.txt", "t.fern"}},
	} {
		if out, errOut, code := lintRun(t, tc.files, tc.args...); code != 0 || out != "" {
			t.Errorf("%v with the rule allowed: exit %d, stdout %q, stderr %q", tc.args, code, out, errOut)
		}
	}
}
