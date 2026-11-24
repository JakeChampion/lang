package e2ecompiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// jqSuites holds each suite of jq 1.7 in examples/jq/tests, and its number
// of cases that are not `%%FAIL` cases. Each case must pass.
var jqSuites = []struct {
	file  string
	cases int
}{
	{"jq.test", 428},
	{"onig.test", 40},
	{"manonig.test", 17},
	{"man.test", 224},
}

func TestSelfHostExampleJqUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/jq/jq_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// buildJq compiles the jq command line for this host.
func buildJq(t *testing.T) string {
	t.Helper()
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("no native self-host target")
	}
	src, err := filepath.Abs("../../../examples/jq/jq.fern")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	bin := filepath.Join(t.TempDir(), "jq")
	if out, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build jq: %v\n%s", err, out)
	}
	return bin
}

type jqCase struct {
	line           int
	program, input string
	expected       []string
}

// jqCases reads the cases of jq.test: a program, an input and the expected
// outputs, with a blank line between cases. A `%%FAIL` case expects a compile
// error, and the port does not judge those yet.
func jqCases(t *testing.T, path string) []jqCase {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	var cases []jqCase
	for i := 0; i < len(lines); {
		l := lines[i]
		switch {
		case strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#"):
			i++
		case strings.HasPrefix(l, "%%FAIL"):
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				i++
			}
		default:
			c := jqCase{line: i + 1, program: l, input: lines[i+1]}
			j := i + 2
			for j < len(lines) && strings.TrimSpace(lines[j]) != "" && !strings.HasPrefix(lines[j], "#") {
				c.expected = append(c.expected, lines[j])
				j++
			}
			cases = append(cases, c)
			i = j
		}
	}
	return cases
}

// jqValue decodes a JSON text with each number as an f64, as jq compares
// numbers. A number outside the range of an f64 stays as its text, which jq
// writes in one canonical form.
func jqValue(text string) (any, error) {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return jqNumbers(v), nil
}

func jqNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if f, err := strconv.ParseFloat(string(x), 64); err == nil {
			return f
		}
		return string(x)
	case []any:
		for i := range x {
			x[i] = jqNumbers(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = jqNumbers(x[k])
		}
	}
	return v
}

// jqSuiteExitOK reports whether the exit status of the port is right for a
// case of jq.test. The harness of jq reads the expected outputs and does not
// look at an error after them, so a case can end in an error. With a jq
// binary, the status and the stderr must be those of jq. With no jq binary,
// a status of 5, the status of a filter error, is right after the outputs.
func jqSuiteExitOK(c jqCase, code int, stderr, ref, modules string) bool {
	if code == 0 {
		return true
	}
	if ref == "" {
		return code == 5
	}
	cmd := exec.Command(ref, "-L", modules, "-c", "--", c.program)
	cmd.Env = jqSuiteEnv()
	cmd.Stdin = strings.NewReader(c.input)
	var refErr bytes.Buffer
	cmd.Stderr = &refErr
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode() == code && refErr.String() == stderr
}

// jqSuiteEnv is the environment of the harness of jq: man.test reads PAGER.
func jqSuiteEnv() []string {
	return append(os.Environ(), "PAGER=less")
}

// TestSelfHostExampleJqSuite runs each case of the suites of jq 1.7 as the
// harness of jq does, with the modules of jq.test on the search path.
func TestSelfHostExampleJqSuite(t *testing.T) {
	bin := buildJq(t)
	ref, _ := exec.LookPath("jq")
	modules, err := filepath.Abs("../../../examples/jq/tests/modules")
	if err != nil {
		t.Fatal(err)
	}
	for _, suite := range jqSuites {
		t.Run(suite.file, func(t *testing.T) {
			passed, total := 0, 0
			for _, c := range jqCases(t, "../../../examples/jq/tests/"+suite.file) {
				total++
				if jqSuiteCase(t, bin, ref, modules, c) {
					passed++
				}
			}
			t.Logf("%s: %d of %d cases pass", suite.file, passed, total)
			if total != suite.cases {
				t.Errorf("%s has %d cases, not the %d of jqSuites", suite.file, total, suite.cases)
			}
		})
	}
}

// jqSuiteCase runs one case, and gives whether it passes.
func jqSuiteCase(t *testing.T, bin, ref, modules string, c jqCase) bool {
	t.Helper()
	cmd := exec.Command(bin, "-L", modules, "-c", "--", c.program)
	cmd.Env = jqSuiteEnv()
	cmd.Stdin = strings.NewReader(c.input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	var got, want []any
	for _, l := range strings.Split(stdout.String(), "\n") {
		if l == "" {
			continue
		}
		v, err := jqValue(l)
		if err != nil {
			t.Errorf("line %d: %q gives the output %q, which is not JSON", c.line, c.program, l)
			return false
		}
		got = append(got, v)
	}
	for _, l := range c.expected {
		v, err := jqValue(l)
		if err != nil {
			t.Fatalf("line %d: the expected output %q is not JSON", c.line, l)
		}
		want = append(want, v)
	}
	if !jqSuiteExitOK(c, cmd.ProcessState.ExitCode(), stderr.String(), ref, modules) || !reflect.DeepEqual(got, want) {
		t.Errorf("line %d: %q on %s: exit %d, got %s, want %v\n%s", c.line, c.program, c.input, cmd.ProcessState.ExitCode(), stdout.String(), c.expected, stderr.String())
		return false
	}
	return true
}

// A function that calls itself goes deeper than the machine stack lets a
// call go. The evaluator leaves the machine stack after a fixed count of
// calls, so each program here goes through that point in a different
// construct. Each expected output is the output of jq 1.7.
func TestSelfHostExampleJqDeepRecursion(t *testing.T) {
	bin := buildJq(t)
	for _, c := range []struct{ program, want string }{
		{"def f: if . == 0 then 0 else (. - 1 | f) end; 3000000 | f", "0"},
		{"def f: if . == 0 then 0 else (. - 1 | f) + 1 end; 3000000 | f", "3000000"},
		{"def f($n): if $n == 0 then . else f($n - 1) end; 7 | f(1000000)", "7"},
		{"def f: ., (. + 1 | f); first(0 | f | select(. >= 3000000))", "3000000"},
		{"def f(g): if . == 0 then g else (. - 1 | f(g)) end; 100000 | f(5)", "5"},
		{"def f: if . == 0 then empty else ., (. - 1 | f) end; [5000 | f] | length", "5000"},
		{"def f: if . == 0 then 0 else try (. - 1 | f) catch . end; 5000 | f", "0"},
		{"def f: if . == 0 then error(\"x\") else (. - 1 | f) end; try (5000 | f) catch .", "\"x\""},
		{"def f: if . == 0 then error(\"x\") else (. - 1 | f)? end; [5000 | f]", "[]"},
		{"def f: if . == 0 then 0 else [. - 1 | f] | .[0] + 1 end; 5000 | f", "5000"},
		{"def f: if . == 0 then 0 else {a: (. - 1 | f)} | .a + 1 end; 5000 | f", "5000"},
		{"def f: if . == 0 then null else (. - 1 | f) // 1 end; 5000 | f", "1"},
		{"def f: if . == 0 then 0 else reduce (. - 1 | f) as $x (1; . + $x) end; 5000 | f", "5000"},
		{"def f: if . == 0 then 0 else foreach (. - 1 | f) as $x (1; . + $x) end; 5000 | f", "5000"},
		{"def f: if . == 0 then 0 else (. - 1 | f) as $x | $x + 1 end; 5000 | f", "5000"},
		{"def f: if . == 0 then 0 else first(. - 1 | f) + 1 end; 5000 | f", "5000"},
		{"def f: if . == 0 then 0 else label $l | (. - 1 | f), break $l end; 5000 | f", "0"},
		{"def f: if . == 0 then \"\" else \"\\(. - 1 | f)\" end; 5000 | f", "\"\""},
		{"def f: ., (. + 1 | f); [label $out | 0 | f | if . == 5000 then break $out else . end] | length", "5000"},
		{"def f: ., (. + 1 | f); first(0 | f | select(. >= 5000))", "5000"},
		{"def f: ., (. + 1 | f); [limit(3; 0 | f | select(. > 4000))]", "[4001,4002,4003]"},
		{"def f: ., (. + 1 | f); isempty(0 | f | select(. > 4000))", "false"},
		{"def f: if type == \"object\" then .a | f else . end; reduce range(3000) as $i (1; {a: .}) | [path(f)] | .[0] | length", "3000"},
		{"def f: if type == \"object\" then .a | f else . end; reduce range(3000) as $i (1; {a: .}) | (f |= 2) | [paths] | length", "3000"},
		{"def f: if . == 0 then 0 else (. - 1 | f) end; [range(3) | 2000 + . | f]", "[0,0,0]"},
		{"def g: if . == 0 then 0 else (. - 1 | g) end; def f: if . == 0 then 0 else (2000 | g | empty), (. - 1 | f) end; 3000 | f", "0"},
		{"def f(g): g; def h: if . >= 5000 then . else ., (. + 1 | f(h)) end; [0 | h] | length", "5001"},
		{"def loop(f): if . >= 3000 then . else f | loop(f) end; 0 | loop(. + 1)", "3000"},
		{"def f($x): $x; def g: if . == 0 then 0 else f(. - 1 | g) + 1 end; 5000 | g", "5000"},
		{"def f: def g: if . == 0 then 0 else (. - 1 | f) end; g; 5000 | f", "0"},
		{"def f: if . == 0 then 0 else (. - 1 | f) end; reduce range(3000) as $i (0; . + ($i | f))", "0"},
	} {
		out, err := exec.Command(bin, "-n", "-c", c.program).CombinedOutput()
		if got := strings.TrimSpace(string(out)); err != nil || got != c.want {
			if len(got) > 300 {
				got = got[:300]
			}
			t.Errorf("%q: got %s (%v), want %s", c.program, got, err, c.want)
		}
	}
}

// The command line gives the stdout, the stderr and the exit status of jq 1.7.
func TestSelfHostExampleJqAgainstJq(t *testing.T) {
	ref, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("no jq on PATH")
	}
	bin := buildJq(t)
	cases := []struct {
		args  []string
		input string
	}{
		{[]string{"."}, "{\"b\":[1,{}],\"a\":[],\"c\":\"x\\u0001\\u007f\u00e9/\"}"},
		{[]string{"-c", "."}, "1 2 {\"a\":1}[3]"},
		{[]string{"-c", ".[]"}, "1\n2\n"},
		{[]string{"-c", ".[]"}, "\n\n\"a\"\n\n"},
		{[]string{"-r", "."}, "\"a\\nb\""},
		{[]string{"-s", "-c", "."}, "1 2"},
		{[]string{"-n", "-c", "--arg", "x", "1", "[$x, $x]"}, ""},
		{[]string{"-cn", "[1e1000, 1.000, 0.10, -(1.000)]"}, ""},
		{[]string{"-c", "[.[0] == .[1], .[0] == (.[1]|.+0), (map(.+0)|unique), (.+[1+0]|sort), .[2]+0]"}, "[1.0000000000000001, 1, 836325471441701108668]"},
		{[]string{"-c", "., map(. == infinite)"}, "[1e1000000000, 0e-2000000000, 25e-1147483647, 1e999999999]"},
		{[]string{"-c", ".a"}, "5"},
		{[]string{"-c", ".a"}, "{\"a\":1}\n5\n{\"a\":2}"},
		{[]string{"-c", ".[] | .x?"}, "[1,{\"x\":2}]"},
		{[]string{"-c", "$x"}, "null"},
		{[]string{"-c", "{a: (1,2), b: (3,4)}"}, "null"},
		{[]string{"-c", "to_entries | map(select(.value > 1)) | from_entries"}, "{\"a\":1,\"b\":2,\"c\":3}"},
		{[]string{"-c", "[.[] | .a // \"none\"] , (map(.a) | add)"}, "[{\"a\":1},{},{\"a\":2.5}]"},
		{[]string{"-cn", "[1 + 2 * 3 - 4 / 2 % 3, 0.1 + 0.2, 1e17 + 0, -9.7 % 3, \"ab\" * 2, \"a,b\" / \",\"]"}, ""},
		{[]string{"-c", ".[] | length"}, "[[1],\"a\u00e9\",-5,true,3]"},
		{[]string{"-c", ".a + 1"}, "{\"a\":\"x\"}\n{\"a\":1}"},
		{[]string{"-cn", "1 % 0"}, ""},
		{[]string{"-c", "keys, has(\"a\"), (.a | has(0)), has(0)"}, "{\"b\":1,\"a\":[2]}"},
		{[]string{"-r", "map(tostring) | add"}, "[1,\"a\",[1.0],null]"},
		{[]string{"-c", "if . == 1 then error({\"a\": .}) elif . == 2 then error(\"two\") else try error(\"x\") catch . end"}, "1\n2\n3\n"},
		{[]string{"-c", "reduce .[] as $x (0; . + $x), [foreach .[] as $x (0; . + $x; [$x, .])]"}, "[1,2,3]\n"},
		{[]string{"-c", "[.. | numbers] | sort, unique, min, max, (map(tostring) | join(\",\")), [limit(2; .[])]"}, "{\"a\":[3,{\"b\":[1,3.0]}],\"c\":2}\n"},
		{[]string{"-c", "flatten(1), any, all, reverse, first, last, [range(length)]"}, "[[1,[2]],null,\"a\"]\n"},
		{[]string{"-c", "[sort_by(.k), group_by(.k), unique_by(.k), min_by(.k), max_by(.k)]"}, `[null,{},{"k":1},{"k":1.0},{"k":[1]},{"k":[1,0]},{"k":{"a":1}}]`},
		{[]string{"-c", "group_by(.k)"}, `[{"k":1},7]`},
		{[]string{"-c", "sort"}, "{\"a\":1}\n[2,1]\n"},
		{[]string{"-c", "."}, "1 2\n[1,\n]"},
		{[]string{"-c", "."}, "1,2"},
		{[]string{"-c", "."}, "{\"a\":1}} 3"},
		{[]string{"-sc", "."}, "1 [2"},
		{[]string{"-c", "."}, "[nan, Infinity, 1., .5, +1, 01] \"\\ud83d\\ude00\" \"a\xffb\" tru"},
		{[]string{"-c", "tojson | fromjson, (.[0] | tostring | tonumber), ([.[1], 233] | implode)"}, "[1.50, 97]"},
		{[]string{"-c", "fromjson"}, "\"[1,\"\n\"\"\n\"1 2\""},
		{[]string{"-r", "@csv \"row: \\(.)\", @tsv, @sh, (map(tostring) | @base64 \"\\(.[1])\"), @html \"<\\(.[1])>\""}, "[1,\"a b\\t<c>'\",null]\n"},
		{[]string{"-c", "\"\\(.a)-\\(.b[])\", @uri, (.a | @base64d)"}, "{\"a\":\"aGk=\",\"b\":[1,2]}\n"},
		{[]string{"-c", "@nosuch"}, "1"},
		{[]string{"-c", ".a.b |= . + 1 | .c //= [] | .c += [.a.b] | del(.d[0, -1]) | [paths(numbers)]"}, "{\"a\":{\"b\":1},\"d\":[1,2,3]}\n"},
		{[]string{"-c", "[.[].a?], [.[] | .a?], (.[2].a.b? // \"none\")"}, "[1,{\"a\":2},{\"a\":{\"b\":3}}]\n"},
		{[]string{"-c", ".[].a.b?"}, "[{\"a\":{\"b\":1}},5]\n"},
		{[]string{"-c", "path(.a | tostring), (.a = 1)"}, "{\"a\":[1]}\n"},
		{[]string{"-c", "def fac: if . <= 1 then 1 else . * (. - 1 | fac) end; def twice(f): f | f; def add($n): . + $n; map(fac), (.[0] | twice(add(10))), [.[] | add(1, 2)]"}, "[3,5]\n"},
		{[]string{"-c", "walk(if type == \"object\" then del(.x) else . end), (.. |= (numbers |= . * 2))"}, "[{\"x\":1,\"y\":[2,{\"x\":3}]},4]\n"},
		{[]string{"-c", "group_by(.k) | map({k: .[0].k, n: length}), (sort_by(.v) | map(.v)), max_by(.v), unique_by(.k)"}, "[{\"k\":\"b\",\"v\":2},{\"k\":\"a\",\"v\":3},{\"k\":\"b\",\"v\":1}]\n"},
		{[]string{"-c", "debug, debug(\"m\", .[0]) | .[] as [$a, {b: $c}] ?// $a | [$a, $c]"}, "[[1,{\"b\":2}],3]\n"},
		{[]string{"-c", "gmtime, todate, (gmtime | mktime), strftime(\"%A, %B %d, %Y %j %U %G-W%V\"), (todate | fromdate), (todate | strptime(\"%Y-%m-%dT%H:%M:%SZ\"))"}, "1425599507\n-86400.5\n"},
		{[]string{"-c", "strptime(\"%d %b %Y\")"}, "\"5 Mar 2015\"\n\"31 Feb 2015\"\n\"x\"\n"},
		{[]string{"-c", "map(sin, cos, atan), atan2(.[0]; .[1]), (builtins | length > 100)"}, "[0, 0.5, 2]\n"},
		{[]string{"-c", "[label $out | .[] | if . > 2 then break $out else . end], pick(.[1]), transpose?, {$__loc__}"}, "[1,2,3]\n[[1],[2,3]]\n"},
		{[]string{"-c", "ascii_upcase, split(\", \"), indices(\", \"), explode[0:2], contains(\"b, c\"), ltrimstr(\"a, \")"}, "\"a, b, c\"\n5\n"},
		{[]string{"-c", "with_entries(select(.value | contains([1])))"}, "{\"a\":[1,2],\"b\":[3]}\n"},
		{[]string{"-c", "[nan, infinite, (. - .), (.[0]? // nan)] | sort, map(isnan), (.[0] < .[1])"}, "1e1000\n"},
		{[]string{"-c", "map(floor, ceil, round, sqrt, fabs), [pow(.[0]; .[1])], .[nan]"}, "[2.5,-1.5]\n"},
		{[]string{"-R", "-c", "[., input_line_number]"}, "a\nb\nc"},
		{[]string{"-R", "-s", "."}, "a\nb\n"},
		{[]string{"-sn", "-c", "[inputs]"}, "1 2"},
		{[]string{"-n", "-c", "[inputs]"}, "1 2 3"},
		{[]string{"-c", "[., input]"}, "1 2 3"},
		{[]string{"-n", "input"}, ""},
		{[]string{"-j", ".[]"}, "[\"a\", 1, \"b\"]"},
		{[]string{"-a", "-c", "."}, "\"é€😀\""},
		{[]string{"-S", "-c", "."}, "{\"b\":{\"z\":1,\"a\":2},\"a\":[{\"y\":1,\"x\":2}]}"},
		{[]string{"-C", "."}, "{\"a\":[1,\"x\",null,true]}"},
		{[]string{"--tab", "."}, "{\"a\":[1]}"},
		{[]string{"--indent", "1", "."}, "{\"a\":[1,{\"b\":2}]}"},
		{[]string{"--seq", "-c", "."}, "\u001e1\n\u001e[2]\n"},
		{[]string{"--stream", "-c", "."}, "{\"a\":[1,{\"b\":null}],\"c\":\"x\"}"},
		{[]string{"--raw-output0", "."}, "[\"a\",\"b\"]"},
		{[]string{"-c", "--arg", "x", "1", "--argjson", "y", "{\"a\":2}", "[$x, $y, $ARGS.named]"}, "null"},
		{[]string{"-n", "-c", "--args", "$ARGS", "a", "b"}, ""},
		{[]string{"-n", "-c", "--jsonargs", "$ARGS.positional", "1", "{\"a\":2}"}, ""},
		{[]string{"-n", "{\"a\":1} | halt_error(3)"}, ""},
		{[]string{"-n", "\"bye\\n\" | halt_error(1)"}, ""},
		{[]string{"-n", "-c", "1, halt, 2"}, ""},
		{[]string{"-n", "\"err\" | stderr"}, ""},
		{[]string{"-e", "."}, "null"},
		{[]string{"-e", "empty"}, "1"},
		{[]string{"-n", "-c", "[limit(3; repeat(1))], first(range(10; infinite)), [limit(-1; 1, 2)]"}, ""},
		{[]string{"-n", "-c", "label $x | try (break $x) catch ."}, ""},
		{[]string{"-n", "-c", "[first(1)] | label $x | try (break $x) catch ."}, ""},
		{[]string{"-n", "1 +"}, ""},
		{[]string{"-n", "[1, 2) | (3"}, ""},
		{[]string{"-n", "{a: 1, b: }"}, ""},
		{[]string{"-n", "trim, ltrim, $nosuch"}, ""},
		{[]string{"-c", "[splits(\"a+\"; \"g\")], test(\"B\"; \"i\"), [match(\"(?<x>a)|b\"; \"g\") | .captures]"}, "\"xaaBab\""},
		{[]string{"-c", "gsub(\"(?<l>[a-z])\"; \"\\(.l|ascii_upcase)\"), [scan(\"(a)(b)\")], capture(\"(?<n>\\\\d+)\")"}, "\"ab 12 ab\""},
		{[]string{"-c", "test(\"(\")"}, "\"a\""},
		{[]string{"-c", "map(frexp), map(modf), [ldexp(3; 2), scalb(3; 2.5), drem(10; 3), copysign(3; -0)]"}, "[5.5, -3.25]"},
		{[]string{"-c", "map(erf, acosh, nearbyint, logb), (3 | pow10)?"}, "[1, 2.5]"},
		{[]string{"-c", "tostream, ([tostream] | fromstream(.[])), [1 | truncate_stream([[0],1],[[1,0],2],[[1,0]],[[1]])]"}, "{\"a\":[1,2]}"},
		{[]string{"-c", "[combinations], getpath([\"a\"])?, input_filename, $__loc__"}, "[[1,2],[3]]"},
		{[]string{"-c", "[try (1 + (1, error(\"x\")))], [limit(3; 0 + range(1; infinite))], (def f: if . < 2 then . else (. - 1 | f) + (. - 2 | f) end; 15 | f)"}, "null"},
		{[]string{"-c", "input + (1, input)"}, "1 2 3 4"},
		{[]string{"-c", "def r: if . > 0 then . - 1 | r else \"done\" end; 100000 | r"}, "null"},
		{[]string{"--seq", "-c", "."}, ""},
		{[]string{"--seq", "-c", "."}, "1\n\u001e2\n"},
		{[]string{"--seq", "-c", "."}, "\u001e1\u001e2\n\u001e[1,\u001e3\n"},
		{[]string{"--seq", "-c", "."}, "\u001e}\n3\u001e4\n\u001e\"a\u001eb\"\n"},
		{[]string{"--seq", "-c", "."}, "\u001enul\n[1]x 5"},
		{[]string{"--seq", "-c", "-n", "[try input catch ., try input catch .]"}, "\u001e\u001e1"},
		{[]string{"-n", "\"\\ \""}, ""},
		{[]string{"-n", "{\"a\\qb\": 1}"}, ""},
		{[]string{"-n", "\"\\(1)\\u12\""}, ""},
		{[]string{"-n", "include \"\\ \"; 0"}, ""},
		{[]string{"-n", "{(0):1}"}, ""},
		{[]string{"-n", "{(1+1):1}"}, ""},
		{[]string{"-n", "{([1,2,3,4,5,6,7,8]):1}"}, ""},
		{[]string{"-n", ". as {(null):$a} | $a"}, ""},
		{[]string{"-n", "-c", "{(\"a\"+\"b\"):1}"}, ""},
		{[]string{"--run-tests"}, "1\n1\n1\n\n.[\nnull\n\n%%FAIL\n.[\njq: error: x\n\n%%FAIL IGNORE MSG\n.[\nx\n\n%%FAIL\n.\nx\n\n.\n1 2\n\n.\n1\nbad\n1\n\n.[]\n[1,2,3]\n1\n2\n\n.[]\n[1]\n1\n2\n3\n\n"},
		{[]string{"--run-tests"}, "1,error(\"x\")\nnull\n1\n2\n\nrepeat(1)\nnull\n1\n1\n\n[.,1]|debug|stderr|length\nnull\n2\n\nget_search_list, get_prog_origin\nnull\n[]\n\n\"x\"|halt_error\nnull\n\n"},
		{[]string{"--run-tests", "--skip", "2", "--take", "1"}, "1\nnull\n1\n\n2\nnull\n2\n\n3\nnull\n3\n\n4\nnull\n4\n"},
		{[]string{"--run-tests", "--skip", "9"}, "1\nnull\n1\n"},
		{[]string{"--run-tests", "no-such-file"}, ""},
	}
	modules, err := filepath.Abs("../../../examples/jq/tests/modules")
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"import \"c\" as c; [c::c, c::a]", "import \"data\" as $d; $d, $d::d", "\"c\" | modulemeta", "get_search_list", "import \"nosuch\" as x; 1"} {
		cases = append(cases, struct {
			args  []string
			input string
		}{[]string{"-n", "-c", "-L", modules, program}, ""})
	}
	// jq --run-tests on its own test files.
	for _, suite := range jqSuites {
		file, err := filepath.Abs("../../../examples/jq/tests/" + suite.file)
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct {
			args  []string
			input string
		}{[]string{"-L", modules, "--run-tests", file}, ""})
	}
	run := func(path, dir string, args []string, input string) (string, string, int) {
		cmd := exec.Command(path, args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}
	compare := func(args []string, input, dir string) {
		wantOut, wantErr, wantCode := run(ref, dir, args, input)
		gotOut, gotErr, gotCode := run(bin, dir, args, input)
		if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
			t.Errorf("jq %q on %q:\ngot  exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", args, input, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
		}
	}
	for _, c := range cases {
		compare(c.args, c.input, "")
	}
	// jq reads its files as one text: a value or a raw line can go on in the
	// next file, a byte order mark counts only before the first byte, and a
	// line of an error counts from the start of the first file.
	fileCases := []struct {
		args  []string
		files []string
	}{
		{[]string{"-c", "[., input_filename, input_line_number]"}, []string{"1", "2\n"}},
		{[]string{"-c", "[., input_filename, input_line_number]"}, []string{"[1,\n2", "", ",\n3]\n"}},
		{[]string{"-c", "."}, []string{"\"ab", "c\"\n", "tr", "ue 5"}},
		{[]string{"-c", "."}, []string{"1\n2\n3\n", "4\n}\n"}},
		{[]string{"-c", "."}, []string{"", "\xef\xbb\xbf2\n"}},
		{[]string{"-c", "."}, []string{"1\n", "\xef\xbb\xbf2\n"}},
		{[]string{"-R", "-c", "[., input_filename, input_line_number]"}, []string{"ab", "cd\nef", ""}},
		{[]string{"-R", "-c", "."}, []string{"\"\xc3", "\xa9\"\n", "\xef\xbb\xbfx\n"}},
		{[]string{"-n", "[inputs] | error(\"x\")"}, []string{"1\n2\n"}},
		{[]string{"-n", "[inputs] | error(\"x\")"}, []string{"1\n2"}},
		{[]string{"-n", "[inputs], (try input catch .), error(\"y\")"}, []string{"1\n", ""}},
		{[]string{"-n", "[inputs]"}, []string{"1", "4\n}\n"}},
		{[]string{"-c", "error(\"e\")"}, []string{"1\n2\n3\n4", ""}},
		{[]string{"--seq", "-c", "."}, []string{"\x1e1", "2\n\x1e3\n"}},
	}
	jqSymlinks(t, compare)
	t.Run("relative library without PWD", func(t *testing.T) {
		t.Setenv("PWD", "")
		compare([]string{"-cn", "-L", "../../../examples/jq/tests/modules", `import "c" as foo; foo::c`}, "", "")
	})
	t.Run("relative library with stale PWD", func(t *testing.T) {
		actual, stale := t.TempDir(), t.TempDir()
		for dir, value := range map[string]string{actual: "actual", stale: "stale"} {
			if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "lib", "m.jq"), []byte("def value: "+strconv.Quote(value)+";"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Chdir(actual)
		t.Setenv("PWD", stale)
		compare([]string{"-nc", "-L", "lib", `include "m"; value`}, "", "")
	})
	for _, c := range fileCases {
		dir := t.TempDir()
		args := append([]string{}, c.args...)
		for i, text := range c.files {
			name := fmt.Sprintf("f%d", i+1)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			args = append(args, name)
		}
		compare(args, "", dir)
	}
}

// jqSymlinks checks that a module path and a -L directory follow symbolic
// links as C realpath does: a module that a link names imports its own
// modules from the directory of the file that the link names.
func jqSymlinks(t *testing.T, compare func(args []string, input, dir string)) {
	dir := t.TempDir()
	files := map[string]string{
		"real/lib/m.jq":    "def f: \"real-m\";\n",
		"elsewhere/m2.jq":  "import \"n\" as n {search: \".\"};\ndef g: n::h;\n",
		"elsewhere/n.jq":   "def h: \"elsewhere-n\";\n",
		"lib2/n.jq":        "def h: \"lib2-n\";\n",
		"elsewhere/bad.jq": "def f: 1;\n.\n",
		"real/prog.jq":     "import \"m2\" as m {search: \"./lib2\"};\nm::g, get_prog_origin\n",
	}
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"link": "real", "lib2/m2.jq": "../elsewhere/m2.jq", "lib2/bad.jq": "../elsewhere/bad.jq", "real/lib2": "../lib2"} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"-L", "link/lib", "-n", "-c", "import \"m\" as m; m::f"},
		{"-L", "link/lib", "-n", "-c", "get_search_list"},
		{"-L", "./link//lib/../lib", "-n", "-c", "get_search_list"},
		{"-L", "nonexist", "-n", "-c", "get_search_list"},
		{"-L", "lib2", "-n", "-c", "import \"m2\" as m; m::g"},
		{"-L", "lib2", "-n", "-c", "import \"bad\" as m; 1"},
		{"-L", "lib2", "-n", "-c", "\"m2\" | modulemeta"},
		{"-n", "-c", "-f", "link/prog.jq"},
		{"-n", "-c", "get_prog_origin"},
	} {
		compare(args, "", dir)
	}
}
