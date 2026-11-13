package e2ecompiler

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// jqSuiteFloor is the number of cases of jq.test that the port passes. A case
// in a part of the language the port does not have yet is skipped, and every
// other case must pass.
const jqSuiteFloor = 415

const jqNotYet = "is not in this port yet"

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
func jqSuiteExitOK(c jqCase, code int, stderr, ref string) bool {
	if code == 0 {
		return true
	}
	if ref == "" {
		return code == 5
	}
	cmd := exec.Command(ref, "-c", "--", c.program)
	cmd.Stdin = strings.NewReader(c.input)
	var refErr bytes.Buffer
	cmd.Stderr = &refErr
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode() == code && refErr.String() == stderr
}

func TestSelfHostExampleJqSuite(t *testing.T) {
	bin := buildJq(t)
	ref, _ := exec.LookPath("jq")
	passed, skipped := 0, 0
	for _, c := range jqCases(t, "../../../examples/jq/tests/jq.test") {
		cmd := exec.Command(bin, "-c", "--", c.program)
		cmd.Stdin = strings.NewReader(c.input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		if strings.Contains(stderr.String(), jqNotYet) {
			skipped++
			continue
		}
		var got, want []any
		for _, l := range strings.Split(stdout.String(), "\n") {
			if l == "" {
				continue
			}
			v, err := jqValue(l)
			if err != nil {
				t.Fatalf("line %d: %q gives the output %q, which is not JSON", c.line, c.program, l)
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
		if !jqSuiteExitOK(c, cmd.ProcessState.ExitCode(), stderr.String(), ref) || !reflect.DeepEqual(got, want) {
			t.Errorf("line %d: %q on %s: exit %d, got %s, want %v\n%s", c.line, c.program, c.input, cmd.ProcessState.ExitCode(), stdout.String(), c.expected, stderr.String())
			continue
		}
		passed++
	}
	t.Logf("jq.test: %d cases pass, %d are in parts of the language that the port does not have yet", passed, skipped)
	if passed < jqSuiteFloor {
		t.Errorf("%d cases of jq.test pass, fewer than the %d of jqSuiteFloor", passed, jqSuiteFloor)
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
	}
	for _, c := range cases {
		run := func(path string) (string, string, int) {
			cmd := exec.Command(path, c.args...)
			cmd.Stdin = strings.NewReader(c.input)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			_ = cmd.Run()
			return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
		}
		wantOut, wantErr, wantCode := run(ref)
		gotOut, gotErr, gotCode := run(bin)
		if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
			t.Errorf("jq %q on %q:\ngot  exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", c.args, c.input, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
		}
	}
}
