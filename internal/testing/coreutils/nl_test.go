package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nlFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func init() {
	registerCorpus("nl", nlCases)
}

func nlCases(t *testing.T) []invocation {
	dir := t.TempDir()
	s4 := nlFile(t, dir, "s4", "a\nb\n\nc\n")
	two := nlFile(t, dir, "two", "p\nq\n")
	two2 := nlFile(t, dir, "two2", "r\ns\n")
	threep := nlFile(t, dir, "threep", "p\nq\np\np\n")
	sec := nlFile(t, dir, "sec", "A\n\\:\\:\\:\nH1\n\\:\\:\nB1\nB2\n\\:\nF1\n\\:\\:\\:\nH2\n\\:\\:\nB3\n")
	body := nlFile(t, dir, "body", "A\n\\:\\:\nB\n")
	foot := nlFile(t, dir, "foot", "A\n\\:\nB\n")
	near := nlFile(t, dir, "near", "A\n\\:\\:\\:x\nB\n\\:\\:\\:\n\\:\\: \nC\n")
	abc := nlFile(t, dir, "abc", "A\nabcabcabc\nH\nabcabc\nB\nabc\nF\n")
	xcolon := nlFile(t, dir, "xcolon", "A\nx:x:x:\nH\nx:x:\nB\nx:\nF\n")
	blanks := nlFile(t, dir, "blanks", "a\n\n\n\n\n\nb\n")
	nonl := nlFile(t, dir, "nonl", "a\nb")
	empty := nlFile(t, dir, "empty", "")
	re := nlFile(t, dir, "re", "abc\nxbc\na|b\naab\na+b\na?b\nab\n")
	raw := nlFile(t, dir, "raw", "a\xffb\nc\n")
	rawname := rawByteFile(t, dir, "x\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	big := nlFile(t, dir, "big", strings.Repeat("0123456789\n", 5000))

	return []invocation{
		{name: "default", args: []string{s4}},
		{name: "no operand reads stdin", stdin: "x\ny\n"},
		{name: "lone dash is stdin", args: []string{"-"}, stdin: "x\ny\n"},
		{name: "two files number continuously", args: []string{two, two2}},
		{name: "unterminated last line", args: []string{nonl}},
		{name: "empty file", args: []string{empty}},
		{name: "line that is not valid UTF-8", args: []string{raw}},
		{name: "large output", args: []string{big}},

		{name: "body all", args: []string{"-ba", s4}},
		{name: "body none", args: []string{"-bn", s4}},
		{name: "body nonempty", args: []string{"-bt", s4}},
		{name: "body with a space", args: []string{"-b", "a", s4}},
		{name: "body long", args: []string{"--body-numbering=a", s4}},
		{name: "body long with a space", args: []string{"--body", "a", s4}},
		{name: "only the first byte of the style is read", args: []string{"-b", "ax", s4}},
		{name: "only the first byte, t", args: []string{"-b", "tx", s4}},
		{name: "only the first byte, n", args: []string{"-b", "nx", s4}},
		{name: "invalid body style", args: []string{"-bx", s4}},
		{name: "empty body style", args: []string{"-b", "", s4}},
		{name: "body style takes the next operand", args: []string{"-b", s4}},
		{name: "invalid header style", args: []string{"-h", "x", s4}},
		{name: "invalid footer style", args: []string{"-f", "x", s4}},
		{name: "header all", args: []string{"-ha", sec}},
		{name: "footer all", args: []string{"-fa", sec}},
		{name: "all three all", args: []string{"-ha", "-ba", "-fa", sec}},
		{name: "header none body none", args: []string{"-hn", "-bn", "-fn", sec}},

		{name: "regex bare p matches every line", args: []string{"-b", "p", s4}},
		{name: "regex literal", args: []string{"-bpa", re}},
		{name: "regex anchored at the start", args: []string{"-bp^a", re}},
		{name: "regex anchored at the end", args: []string{"-bpc$", re}},
		{name: "regex alternation", args: []string{"-bpa\\|x", re}},
		{name: "regex plus is a literal", args: []string{"-bpa+", re}},
		{name: "regex backslash plus repeats", args: []string{"-bpa\\+", re}},
		{name: "regex question is a literal", args: []string{"-bpa?", re}},
		{name: "regex backslash question", args: []string{"-bpa\\?", re}},
		{name: "regex interval", args: []string{"-bpa\\{2\\}", re}},
		{name: "regex brace is a literal", args: []string{"-bpa{2}", re}},
		{name: "regex backreference", args: []string{"-bp\\(a\\)\\1", re}},
		{name: "regex group", args: []string{"-bpa\\(b\\)", re}},
		{name: "regex character class", args: []string{"-bp[[:alpha:]]", re}},
		{name: "regex word character", args: []string{"-bp\\w", re}},
		{name: "regex word start", args: []string{"-bp\\<a", re}},
		{name: "regex matching empty", args: []string{"-bpx*", re}},
		{name: "regex negated class", args: []string{"-bp[^a]", re}},
		{name: "regex class past the first bytes", args: []string{"-bp[c+?|]", re}},
		{name: "regex alternation of literals", args: []string{"-bpbc\\|a+", re}},
		{name: "regex alternation matching neither branch", args: []string{"-bpzz\\|qq", re}},
		{name: "regex alternation with a class branch", args: []string{"-bpzz\\|[0-9x]", re}},
		{name: "regex alternation with an empty branch", args: []string{"-bpzz\\|", re}},
		{name: "regex alternation with a class before a literal", args: []string{"-bpzz\\|[ax]b", re}},
		{name: "regex alternation of anchored branches", args: []string{"-bp^x\\|^z", re}},
		{name: "regex alternation off its anchor", args: []string{"-bp^b\\|^z", re}},
		{name: "regex alternation inside a group", args: []string{"-bp\\(a\\|x\\)b", re}},
		{name: "regex on empty lines", args: []string{"-bp^$", s4}},
		{name: "regex in the header", args: []string{"-hpH", sec}},
		{name: "regex in the footer", args: []string{"-fpF", sec}},
		{name: "regex is invalid", args: []string{"-bp[", s4}},
		{name: "regex unmatched group", args: []string{"-bpa\\(", s4}},
		{name: "regex bad interval", args: []string{"-bpa\\{2,1\\}", s4}},
		{name: "regex leading interval", args: []string{"-bp\\{1\\}", s4}},
		{name: "regex over a line that is not valid UTF-8", args: []string{"-bp\\xff", raw}},

		{name: "format ln", args: []string{"-n", "ln", s4}},
		{name: "format rn", args: []string{"-n", "rn", s4}},
		{name: "format rz", args: []string{"-n", "rz", s4}},
		{name: "format invalid", args: []string{"-n", "x", s4}},
		{name: "format half", args: []string{"-n", "l", s4}},
		{name: "format with a suffix", args: []string{"-n", "lnx", s4}},
		{name: "format empty", args: []string{"-n", "", s4}},
		{name: "format rz with a negative number", args: []string{"-n", "rz", "-v", "-5", two}},
		{name: "format ln with a number wider than the field", args: []string{"-n", "ln", "-v", "100000", "-w2", two}},
		{name: "format rn with a number wider than the field", args: []string{"-v", "100000", "-w2", two}},
		{name: "format rz with a number wider than the field", args: []string{"-n", "rz", "-v", "100000", "-w2", two}},

		{name: "width three", args: []string{"-w3", s4}},
		{name: "width one", args: []string{"-w1", s4}},
		{name: "width one hundred", args: []string{"-w", "100", two}},
		{name: "width zero", args: []string{"-w0", s4}},
		{name: "width not a number", args: []string{"-w", "x", s4}},
		{name: "width negative", args: []string{"-w", "-1", s4}},
		{name: "width past int", args: []string{"-w", "2147483648", s4}},
		{name: "width past uintmax", args: []string{"-w", "99999999999999999999", s4}},
		{name: "width empty", args: []string{"-w", "", s4}},

		{name: "start at five", args: []string{"-v5", s4}},
		{name: "start negative", args: []string{"-v", "-3", s4}},
		{name: "start not a number", args: []string{"-v", "x", s4}},
		{name: "start empty", args: []string{"-v", "", s4}},
		{name: "start with a plus", args: []string{"-v", "+3", s4}},
		{name: "start past uintmax", args: []string{"-v", "99999999999999999999", s4}},
		{name: "start at intmax", args: []string{"-v", "9223372036854775807", two}},
		{name: "start one below intmax", args: []string{"-v", "9223372036854775806", two}},
		{name: "start past intmax", args: []string{"-v", "9223372036854775808", two}},
		{name: "start at intmin", args: []string{"-v", "-9223372036854775808", two}},
		{name: "start past intmin", args: []string{"-v", "-9223372036854775809", two}},
		{name: "increment two", args: []string{"-i2", s4}},
		{name: "increment zero", args: []string{"-i0", s4}},
		{name: "increment negative", args: []string{"-i", "-1", s4}},
		{name: "increment not a number", args: []string{"-i", "x", s4}},
		{name: "increment at intmax", args: []string{"-i", "9223372036854775807", two}},
		{name: "increment steps over intmax", args: []string{"-v", "9223372036854775806", "-i", "2", two}},
		{name: "increment lands on intmax", args: []string{"-v", "9223372036854775805", "-i", "2", two}},
		{name: "decrement past intmin", args: []string{"-v", "-9223372036854775808", "-i", "-1", two}},
		{name: "overflow is not reached on the last line", args: []string{"-v", "9223372036854775807", "-bn", two}},
		{name: "regex numbering overflows past intmax", args: []string{"-v", "9223372036854775806", "-bpp", threep}},

		{name: "separator colon", args: []string{"-s:", s4}},
		{name: "separator empty", args: []string{"-s", "", s4}},
		{name: "separator two bytes", args: []string{"-s", "::", s4}},
		{name: "separator looks like an option", args: []string{"-s", "-", s4}},
		{name: "separator with a tab", args: []string{"-s", "\t\t", s4}},

		{name: "join two blanks", args: []string{"-ba", "-l2", blanks}},
		{name: "join three blanks", args: []string{"-ba", "-l3", blanks}},
		{name: "join one blank", args: []string{"-ba", "-l1", blanks}},
		{name: "join without -ba does nothing", args: []string{"-l3", blanks}},
		{name: "join zero", args: []string{"-l0", s4}},
		{name: "join not a number", args: []string{"-l", "x", s4}},
		{name: "join past uintmax", args: []string{"-l", "99999999999999999999", s4}},
		{name: "join with a regex style", args: []string{"-bp^$", "-l2", blanks}},

		{name: "sections", args: []string{sec}},
		{name: "sections numbered everywhere", args: []string{"-ha", "-ba", "-fa", sec}},
		{name: "sections without renumbering", args: []string{"-p", "-ha", "-fa", sec}},
		{name: "no-renumber long", args: []string{"--no-renumber", "-ha", "-fa", sec}},
		{name: "body delimiter alone resets", args: []string{body}},
		{name: "footer delimiter alone resets", args: []string{"-fa", foot}},
		{name: "a delimiter with a suffix is text", args: []string{near}},
		{name: "sections carry across files", args: []string{sec, sec}},
		{name: "sections with a starting number", args: []string{"-v", "10", "-ha", "-fa", sec}},

		{name: "delimiter default", args: []string{"-d", "\\:", sec}},
		{name: "delimiter empty disables sections", args: []string{"-d", "", sec}},
		{name: "delimiter of one byte implies a colon", args: []string{"-d", "x", "-ha", "-fa", xcolon}},
		{name: "delimiter of three bytes", args: []string{"-d", "abc", "-ha", "-fa", abc}},
		{name: "delimiter that does not occur", args: []string{"-d", "zz", sec}},
		{name: "delimiter long", args: []string{"--section-delimiter=abc", "-ha", abc}},

		{name: "missing file", args: []string{missing}},
		{name: "missing file then a good one", args: []string{missing, s4}},
		{name: "good file then a missing one", args: []string{s4, missing}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{rawname}, rawByteName: true},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "missing name with a quote", args: []string{filepath.Join(dir, "no'such")}},
		{name: "directory", args: []string{d}},
		{name: "directory then a file", args: []string{d, s4}},
		{name: "dashdash", args: []string{"--", s4}},
		{name: "stdin among files", args: []string{s4, "-"}, stdin: "z\n"},

		{name: "invalid short option", args: []string{"-Z", s4}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "long prefix number-format", args: []string{"--num", "rn", s4}},
		{name: "long prefix n is ambiguous", args: []string{"--n", "rn", s4}},
		{name: "long prefix header", args: []string{"--h", "a", s4}},
		{name: "long prefix body", args: []string{"--bo", "a", s4}},
		{name: "long prefix no-renumber", args: []string{"--no", s4}},
		{name: "flag rejects a glued value", args: []string{"--no-renumber=x", s4}},
		{name: "option needs an argument", args: []string{"-w"}},
		{name: "long option needs an argument", args: []string{"--number-width"}},
		{name: "clustered options", args: []string{"-pba", sec}},
		{name: "options after the operand", args: []string{s4, "-ba"}},
		{name: "posix stops at the operand", args: []string{s4, "-ba"}, env: []string{"POSIXLY_CORRECT=1"}},

		{name: "stdout closed", args: []string{s4}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{s4}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{big}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a usage error", args: []string{"-bx", s4}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},
	}
}

func TestNlParity(t *testing.T) {
	requireParity(t, "nl", nlCases(t))
}

func TestNlHelpVersion(t *testing.T) {
	requireHelp(t, "nl", []string{"--help"}, 0)
	requireHelp(t, "nl", []string{"--hel"}, 0)
	requireVersion(t, "nl", []string{"--version"}, 0)
	requireVersion(t, "nl", []string{"--vers"}, 0)
}
