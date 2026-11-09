package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// sandboxArgValues mirrors the value rules of asmcore.arg_rules: for each
// call the filter checks, the argument index and the values it admits.
type sandboxArgMask struct {
	nr, arg int
	mask    int64
}

type sandboxArgValues struct {
	nr     int
	arg    int
	values []int64
}

var sandboxValueRules = map[string][]sandboxArgValues{
	"x86-64": {
		{16, 1, []int64{21505, 21506, 21507, 21508, 21521, 21523, 21524, 1074041865}},
		{41, 0, []int64{1, 2, 10}},
		{54, 1, []int64{0, 1, 6, 17, 41, 282}},
		{54, 2, []int64{1, 2, 6, 9, 13, 15, 26, 31, 51, 66, 104}},
		{56, 0, []int64{17}},
		{72, 1, []int64{0, 3, 4, 1031, 1032}},
		{157, 0, []int64{1, 4, 56, 61}},
	},
	"arm64": {
		{29, 1, []int64{21505, 21506, 21507, 21508, 21521, 21523, 21524, 1074041865}},
		{25, 1, []int64{0, 3, 4, 1031, 1032}},
		{167, 0, []int64{1, 4, 56, 61}},
		{198, 0, []int64{1, 2, 10}},
		{208, 1, []int64{0, 1, 6, 17, 41, 282}},
		{208, 2, []int64{1, 2, 6, 9, 13, 15, 26, 31, 51, 66, 104}},
		{220, 0, []int64{17}},
	},
}

// sandboxMaskRules are the mask rules of asmcore.arg_rules whose argument a
// runtime source passes: the openat flags. The mapping rules take only a
// literal protection from the heap and guard helpers, so no scan covers them.
var sandboxMaskRules = map[string][]sandboxArgMask{
	"x86-64": {{257, 2, 0x7fe0a03e}},
	"arm64":  {{56, 2, 0x7fe2203e}},
}

// sandboxFlagExpressions names the openat flag expressions of the runtime that
// are not literals, with the union of the bits each one carries on each
// architecture: the open options of __fern_open_flags, the four flag words of
// open_file, and the directory and no-follow bits the dir helpers add.
var sandboxFlagExpressions = map[string]map[string]int64{
	"x86-64": {
		"f as i64":                0x1f5fc1,
		"flags as i64":            0x1f5fc1,
		"(65536 | 524288) as i64": 65536 | 524288,
	},
	"arm64": {
		"f as i64":                0x1ddfc1,
		"flags as i64":            0x1ddfc1,
		"(16384 | 524288) as i64": 16384 | 524288,
	},
}

// sandboxArgExpressions names the argument expressions of the runtime that
// are not literals, with the values each one takes. A new expression here is
// a new decision about the filter, not a typo to allow.
var sandboxArgExpressions = map[string][]int64{
	"af as i64":            {2, 10},
	"(21506 + act) as i64": {21506, 21507, 21508},
	"option as i64":        {56, 61},
	"(op - 1) as i64":      {1, 2},
}

var sandboxRawSyscallRe = regexp.MustCompile(`__syscall[3-6]\((\d+),`)

// TestSelfHostSandboxRulesCoverRuntime reads every raw system call of the
// Linux and Android runtime sources and requires each argument the filter
// checks to be a value the rule admits. A runtime helper that passes a new
// constant is otherwise killed under FERN_SANDBOX=1 on its ordinary path.
func TestSelfHostSandboxRulesCoverRuntime(t *testing.T) {
	dirs := map[string]string{
		"x86-64-linux":   "x86-64",
		"x86-64-android": "x86-64",
		"arm64-linux":    "arm64",
		"arm64-android":  "arm64",
	}
	checked := 0
	for dir, arch := range dirs {
		files, err := filepath.Glob(filepath.Join("../../../compiler/runtime", dir, "*.fern"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no runtime sources under %s: %v", dir, err)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(b), "\n") {
				m := sandboxRawSyscallRe.FindStringIndex(line)
				if m == nil {
					continue
				}
				nr, args := splitRawSyscall(t, line[m[0]:])
				for _, r := range sandboxValueRules[arch] {
					if r.nr != nr {
						continue
					}
					if r.arg >= len(args) {
						t.Errorf("%s:%d: call %d has no argument %d", filepath.Base(f), i+1, nr, r.arg)
						continue
					}
					checked++
					for _, v := range sandboxArgTakes(t, filepath.Base(f), i+1, args[r.arg]) {
						if !hasInt64(r.values, v) {
							t.Errorf("%s:%d: call %d passes %d as argument %d, which the sandbox rule kills; the rule admits %v", filepath.Base(f), i+1, nr, v, r.arg, r.values)
						}
					}
				}
				for _, r := range sandboxMaskRules[arch] {
					if r.nr != nr {
						continue
					}
					if r.arg >= len(args) {
						t.Errorf("%s:%d: call %d has no argument %d", filepath.Base(f), i+1, nr, r.arg)
						continue
					}
					checked++
					v := sandboxFlagsTake(t, arch, filepath.Base(f), i+1, args[r.arg])
					if v&r.mask != 0 {
						t.Errorf("%s:%d: call %d passes %#x as argument %d, which carries a bit of the sandbox mask %#x", filepath.Base(f), i+1, nr, v, r.arg, r.mask)
					}
				}
			}
		}
	}
	if checked < 20 {
		t.Errorf("checked %d arguments, want the runtime's socket, setsockopt, ioctl, prctl and clone sites", checked)
	}
}

// splitRawSyscall returns the number and the argument texts of one
// `__syscallN(nr, a, b, ...)` call, splitting at the commas outside
// parentheses.
func splitRawSyscall(t *testing.T, call string) (int, []string) {
	t.Helper()
	open := strings.Index(call, "(")
	depth, start := 0, open+1
	var parts []string
	for i := open; i < len(call); i++ {
		switch call[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(call[start:i]))
				nr, err := strconv.Atoi(parts[0])
				if err != nil {
					t.Fatalf("%q: syscall number %q is not a literal", call, parts[0])
				}
				return nr, parts[1:]
			}
		case ',':
			if depth == 1 {
				parts = append(parts, strings.TrimSpace(call[start:i]))
				start = i + 1
			}
		}
	}
	t.Fatalf("%q: unterminated call", call)
	return 0, nil
}

// sandboxArgTakes returns the values one argument expression can take: the
// literal itself, or the recorded set for a named expression.
func sandboxArgTakes(t *testing.T, file string, line int, expr string) []int64 {
	t.Helper()
	lit := strings.TrimSuffix(expr, " as i64")
	if v, err := strconv.ParseInt(lit, 10, 64); err == nil {
		return []int64{v}
	}
	if vals, ok := sandboxArgExpressions[expr]; ok {
		return vals
	}
	t.Errorf("%s:%d: argument %q is neither a literal nor a recorded expression; add it to sandboxArgExpressions with the values it takes", file, line, expr)
	return nil
}

// sandboxFlagsTake returns the bits one openat flag expression can carry on
// `arch`: the literal itself, or the recorded union for a named expression.
func sandboxFlagsTake(t *testing.T, arch, file string, line int, expr string) int64 {
	t.Helper()
	lit := strings.TrimSuffix(expr, " as i64")
	if v, err := strconv.ParseInt(lit, 10, 64); err == nil {
		return v
	}
	if v, ok := sandboxFlagExpressions[arch][expr]; ok {
		return v
	}
	t.Errorf("%s:%d: openat flags %q are neither a literal nor a recorded expression; add it to sandboxFlagExpressions with the bits it carries", file, line, expr)
	return 0
}

func hasInt64(xs []int64, v int64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
